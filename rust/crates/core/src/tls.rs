//! Retained certificate pairs and Go-compatible leaf certificate pinning.
use crate::{Error, Result, files::create_synced};
use rustls::{
    DigitallySignedStruct, SignatureScheme,
    client::danger::{HandshakeSignatureValid, ServerCertVerified, ServerCertVerifier},
    pki_types::{CertificateDer, ServerName, UnixTime},
};
use sha2::{Digest, Sha256};
use std::{
    path::{Path, PathBuf},
    sync::Arc,
};
pub struct CertificateFiles {
    pub cert: PathBuf,
    pub key: PathBuf,
    pub created: bool,
}
fn internal(e: impl std::fmt::Display) -> Error {
    Error::Internal(e.to_string())
}
impl CertificateFiles {
    pub fn server_config(&self) -> Result<rustls::ServerConfig> {
        let mut cert = std::io::BufReader::new(std::fs::File::open(&self.cert)?);
        let chain = rustls_pemfile::certs(&mut cert).collect::<std::io::Result<Vec<_>>>()?;
        let mut key = std::io::BufReader::new(std::fs::File::open(&self.key)?);
        let key = rustls_pemfile::private_key(&mut key)?
            .ok_or_else(|| internal("PEM file contains no private key"))?;
        rustls::ServerConfig::builder_with_provider(Arc::new(
            rustls::crypto::ring::default_provider(),
        ))
        .with_safe_default_protocol_versions()
        .map_err(internal)?
        .with_no_client_auth()
        .with_single_cert(chain, key)
        .map_err(internal)
    }
}
pub fn certificate_files(
    dir: &Path,
    cert: Option<&Path>,
    key: Option<&Path>,
    hosts: &[&str],
) -> Result<CertificateFiles> {
    let explicit = cert.is_some() || key.is_some();
    let (mut p, generate) = match (cert, key) {
        (None, None) => {
            let p = CertificateFiles {
                cert: dir.join("server.crt"),
                key: dir.join("server.key"),
                created: false,
            };
            let cert_exists = p.cert.try_exists()?;
            let key_exists = p.key.try_exists()?;
            if cert_exists != key_exists {
                return Err(internal(
                    "certificate files: only one of server.crt and server.key exists or is readable",
                ));
            }
            (p, !cert_exists)
        }
        (Some(cert), Some(key)) => (
            CertificateFiles {
                cert: cert.to_owned(),
                key: key.to_owned(),
                created: false,
            },
            false,
        ),
        _ => {
            return Err(Error::Validation(
                "-cert and -key must name both files, or neither for a self-signed pair in the data directory".into(),
            ));
        }
    };
    if explicit {
        std::fs::metadata(&p.cert)?;
        std::fs::metadata(&p.key)?;
    }
    if generate {
        let key = rcgen::KeyPair::generate().map_err(internal)?;
        let mut params = rcgen::CertificateParams::new(
            hosts
                .iter()
                .filter(|h| !h.is_empty())
                .map(|h| h.to_string())
                .collect::<Vec<_>>(),
        )
        .map_err(internal)?;
        params.distinguished_name = rcgen::DistinguishedName::new();
        params
            .distinguished_name
            .push(rcgen::DnType::CommonName, "tam-server");
        params
            .distinguished_name
            .push(rcgen::DnType::OrganizationName, "Ticket Auction Manager");
        let now = time::OffsetDateTime::now_utc();
        params.not_before = now - time::Duration::hours(1);
        params.not_after = now + time::Duration::days(3653);
        params.key_usages = vec![
            rcgen::KeyUsagePurpose::DigitalSignature,
            rcgen::KeyUsagePurpose::KeyEncipherment,
        ];
        params.extended_key_usages = vec![rcgen::ExtendedKeyUsagePurpose::ServerAuth];
        let cert = params.self_signed(&key).map_err(internal)?;
        create_synced(&p.key, key.serialize_pem().as_bytes())?;
        if let Err(e) = create_synced(&p.cert, cert.pem().as_bytes()) {
            let _ = std::fs::remove_file(&p.key);
            return Err(e);
        }
        p.created = true;
    }
    p.server_config()?;
    Ok(p)
}
pub fn fingerprint_of(der: &[u8]) -> String {
    Sha256::digest(der)
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect()
}
/// Verify the exact leaf on every handshake; cryptographic signatures still run.
#[derive(Debug)]
pub(crate) struct LeafVerifier {
    pub pin: Option<String>,
}
impl ServerCertVerifier for LeafVerifier {
    fn verify_server_cert(
        &self,
        end: &CertificateDer<'_>,
        _intermediates: &[CertificateDer<'_>],
        _name: &ServerName<'_>,
        _ocsp: &[u8],
        _now: UnixTime,
    ) -> std::result::Result<ServerCertVerified, rustls::Error> {
        let got = fingerprint_of(end.as_ref());
        if self.pin.as_ref().is_some_and(|p| p != &got) {
            return Err(rustls::Error::General(format!(
                "server certificate changed (fingerprint {got}); pair with the server again"
            )));
        }
        Ok(ServerCertVerified::assertion())
    }
    fn verify_tls12_signature(
        &self,
        message: &[u8],
        cert: &CertificateDer<'_>,
        dss: &DigitallySignedStruct,
    ) -> std::result::Result<HandshakeSignatureValid, rustls::Error> {
        rustls::crypto::verify_tls12_signature(
            message,
            cert,
            dss,
            &rustls::crypto::ring::default_provider().signature_verification_algorithms,
        )
    }
    fn verify_tls13_signature(
        &self,
        message: &[u8],
        cert: &CertificateDer<'_>,
        dss: &DigitallySignedStruct,
    ) -> std::result::Result<HandshakeSignatureValid, rustls::Error> {
        rustls::crypto::verify_tls13_signature(
            message,
            cert,
            dss,
            &rustls::crypto::ring::default_provider().signature_verification_algorithms,
        )
    }
    fn supported_verify_schemes(&self) -> Vec<SignatureScheme> {
        rustls::crypto::ring::default_provider()
            .signature_verification_algorithms
            .supported_schemes()
    }
}
pub(crate) fn client_config(pin: Option<&str>) -> Result<rustls::ClientConfig> {
    let normalized = pin.map(|p| p.trim().replace(':', "").to_ascii_lowercase());
    if normalized.as_ref().is_some_and(|p| {
        p.len() != 64
            || !p
                .bytes()
                .all(|b| b.is_ascii_digit() || matches!(b, b'a'..=b'f'))
    }) {
        return Err(Error::Validation("invalid certificate fingerprint".into()));
    }
    Ok(rustls::ClientConfig::builder_with_provider(Arc::new(
        rustls::crypto::ring::default_provider(),
    ))
    .with_safe_default_protocol_versions()
    .map_err(internal)?
    .dangerous()
    .with_custom_certificate_verifier(Arc::new(LeafVerifier { pin: normalized }))
    .with_no_client_auth())
}
pub async fn fingerprint(host_port: &str) -> Result<String> {
    let url = reqwest::Url::parse(&format!("https://{host_port}")).map_err(internal)?;
    let host = url
        .host_str()
        .ok_or_else(|| internal("missing TLS host"))?
        .trim_matches(['[', ']'])
        .to_owned();
    let name = ServerName::try_from(host).map_err(internal)?;
    let connector = tokio_rustls::TlsConnector::from(Arc::new(client_config(None)?));
    let task = async {
        let stream = tokio::net::TcpStream::connect(host_port).await?;
        let stream = connector.connect(name, stream).await?;
        let cert = stream
            .get_ref()
            .1
            .peer_certificates()
            .and_then(|c| c.first())
            .ok_or_else(|| internal("server sent no certificate"))?;
        Ok(fingerprint_of(cert.as_ref()))
    };
    tokio::time::timeout(std::time::Duration::from_secs(5), task)
        .await
        .map_err(internal)?
}
