//! TAM mDNS and bounded, cached subnet discovery.
use crate::{Error, Result, config::host_port, remote::Remote};
use std::{
    collections::{HashMap, HashSet},
    net::{IpAddr, Ipv4Addr},
    sync::{Arc, Mutex, Weak},
    time::{Duration, Instant},
};
pub const SERVICE: &str = "_tam._tcp.local.";
#[derive(Clone, Debug, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
pub struct Server {
    pub name: String,
    pub host: String,
    pub port: String,
    pub tls: bool,
    pub version: String,
}
#[derive(Clone, Debug)]
pub struct Network {
    pub address: Ipv4Addr,
    pub netmask: Ipv4Addr,
}
impl Network {
    fn contains(&self, host: &str) -> bool {
        host.parse::<Ipv4Addr>().is_ok_and(|ip| {
            u32::from(ip) & u32::from(self.netmask)
                == u32::from(self.address) & u32::from(self.netmask)
        })
    }
}
fn internal(e: impl std::fmt::Display) -> Error {
    Error::Internal(e.to_string())
}
pub fn announcement_info(
    name: &str,
    hostname: &str,
    port: u16,
    tls: bool,
    version: &str,
    addresses: &[IpAddr],
) -> Result<mdns_sd::ServiceInfo> {
    let properties = HashMap::from([
        ("port".to_owned(), port.to_string()),
        ("tls".into(), tls.to_string()),
        ("name".into(), name.into()),
        ("v".into(), version.into()),
    ]);
    mdns_sd::ServiceInfo::new(SERVICE, name, hostname, addresses, port, properties)
        .map_err(internal)
}
pub fn skip_interface(name: &str) -> bool {
    let lower = name.to_ascii_lowercase();
    if lower.starts_with("vethernet") {
        return name.contains("(WSL") || name.contains("(Default Switch)");
    }
    [
        "docker", "br-", "virbr", "veth", "lxc", "cni", "flannel", "podman",
    ]
    .iter()
    .any(|p| lower.starts_with(p))
        || name.contains("(WSL")
        || name.contains("(Default Switch)")
}
pub fn local_networks() -> Vec<Network> {
    if_addrs::get_if_addrs()
        .unwrap_or_default()
        .into_iter()
        .filter(|i| i.is_oper_up() && !i.is_loopback() && !skip_interface(&i.name))
        .filter_map(|i| match i.addr {
            if_addrs::IfAddr::V4(a) if !a.ip.is_link_local() => Some(Network {
                address: a.ip,
                netmask: a.netmask,
            }),
            _ => None,
        })
        .collect()
}
pub fn local_addresses() -> Vec<String> {
    let mut out: Vec<_> = local_networks()
        .into_iter()
        .map(|n| n.address.to_string())
        .collect();
    out.sort();
    out.dedup();
    out
}
pub fn sweep_targets(nets: &[Network]) -> Vec<String> {
    let mut out = Vec::new();
    let mut seen = HashSet::new();
    for n in nets {
        let octets = n.address.octets();
        for i in 1..255 {
            if out.len() >= 1024 {
                return out;
            }
            let h = Ipv4Addr::new(octets[0], octets[1], octets[2], i).to_string();
            if seen.insert(h.clone()) {
                out.push(h);
            }
        }
    }
    out
}
pub fn collapse(all: Vec<Server>, local: &[Network]) -> Vec<Server> {
    let mut seen: HashMap<String, Server> = HashMap::new();
    for server in all {
        let key = if server.name == server.host {
            host_port(&server.host, &server.port)
        } else {
            format!("{}|{}|{}", server.name, server.port, server.tls)
        };
        let on_local = |s: &Server| local.iter().any(|n| n.contains(&s.host));
        let keep = seen.get(&key).is_none_or(|prev| {
            let (s, p) = (on_local(&server), on_local(prev));
            if s != p { s } else { server.host < prev.host }
        });
        if keep {
            seen.insert(key, server);
        }
    }
    let mut out: Vec<_> = seen.into_values().collect();
    out.sort_by(|a, b| a.name.cmp(&b.name).then(a.host.cmp(&b.host)));
    out
}
fn daemon() -> Result<mdns_sd::ServiceDaemon> {
    let d = mdns_sd::ServiceDaemon::new().map_err(internal)?;
    for iface in if_addrs::get_if_addrs().unwrap_or_default() {
        if skip_interface(&iface.name)
            && let Err(error) = d.disable_interface(mdns_sd::IfKind::Name(iface.name))
        {
            let _ = d.shutdown();
            return Err(internal(error));
        }
    }
    Ok(d)
}
pub struct Announcement {
    daemon: mdns_sd::ServiceDaemon,
    fullname: String,
}
impl Drop for Announcement {
    fn drop(&mut self) {
        if let Ok(rx) = self.daemon.unregister(&self.fullname) {
            let _ = rx.recv_timeout(Duration::from_secs(1));
        }
        if let Ok(rx) = self.daemon.shutdown() {
            let _ = rx.recv_timeout(Duration::from_secs(1));
        }
    }
}
pub fn announce(name: &str, port: u16, tls: bool, version: &str) -> Result<Announcement> {
    let hostname = machine_hostname();
    let info = announcement_info(
        name,
        &format!("{}.local.", hostname.trim_end_matches('.')),
        port,
        tls,
        version,
        &[],
    )?
    .enable_addr_auto();
    let daemon = daemon()?;
    let fullname = info.get_fullname().to_owned();
    if let Err(error) = daemon.register(info) {
        let _ = daemon.shutdown();
        return Err(internal(error));
    }
    Ok(Announcement { daemon, fullname })
}
pub fn machine_hostname() -> String {
    std::env::var("COMPUTERNAME")
        .or_else(|_| std::env::var("HOSTNAME"))
        .ok()
        .filter(|s| !s.is_empty())
        .or_else(|| {
            std::fs::read_to_string("/etc/hostname")
                .ok()
                .map(|s| s.trim().into())
        })
        .unwrap_or_else(|| "TAM Server".into())
}
pub async fn browse(wait: Duration) -> Result<Vec<Server>> {
    let (session, entries) = BrowseLifetime::start()?;
    receive_browse(session, entries, wait).await
}
// The future owns the only strong reference; the cache holds weak references so
// shutdown can also stop active browsers without extending canceled lifetimes.
struct BrowseLifetime {
    daemon: Mutex<Option<mdns_sd::ServiceDaemon>>,
}
impl BrowseLifetime {
    fn start() -> Result<(Arc<Self>, mdns_sd::Receiver<mdns_sd::ServiceEvent>)> {
        let daemon = daemon()?;
        let session = Arc::new(Self {
            daemon: Mutex::new(Some(daemon.clone())),
        });
        let entries = daemon.browse(SERVICE).map_err(internal)?;
        Ok((session, entries))
    }
    fn stop(&self) {
        let mut owned = self.daemon.lock().unwrap_or_else(|e| e.into_inner());
        if let Some(daemon) = owned.take() {
            let _ = daemon.stop_browse(SERVICE);
            if let Ok(rx) = daemon.shutdown() {
                let _ = rx.recv_timeout(Duration::from_secs(1));
            }
        }
    }
}
impl Drop for BrowseLifetime {
    fn drop(&mut self) {
        self.stop();
    }
}
async fn receive_browse(
    session: Arc<BrowseLifetime>,
    entries: mdns_sd::Receiver<mdns_sd::ServiceEvent>,
    wait: Duration,
) -> Result<Vec<Server>> {
    let deadline = tokio::time::sleep(wait);
    tokio::pin!(deadline);
    let mut found = Vec::new();
    loop {
        tokio::select! {
            _ = &mut deadline => break,
            event = entries.recv_async() => match event {
                Ok(mdns_sd::ServiceEvent::ServiceResolved(info)) => found.push(*info),
                Ok(_) => {},
                Err(_) => break,
            }
        }
    }
    session.stop();
    Ok(discovered_candidates(found))
}
/// Decode resolved mDNS records into the address candidates used by `browse`.
pub fn discovered_candidates(
    entries: impl IntoIterator<Item = mdns_sd::ResolvedService>,
) -> Vec<Server> {
    let mut found = Vec::new();
    for info in entries {
        let mut addresses: Vec<_> = info
            .get_addresses()
            .iter()
            .map(|a| a.to_ip_addr())
            .collect();
        addresses.sort_by_key(|a| (!a.is_ipv4(), a.to_string()));
        let hosts = if addresses.is_empty() {
            vec![info.get_hostname().trim_end_matches('.').to_owned()]
        } else {
            addresses.iter().map(ToString::to_string).collect()
        };
        let instance = info
            .get_fullname()
            .strip_suffix(&format!(".{SERVICE}"))
            .unwrap_or(info.get_fullname());
        for host in hosts {
            found.push(Server {
                name: info.get_property_val_str("name").unwrap_or(instance).into(),
                host,
                port: info
                    .get_property_val_str("port")
                    .map(str::to_owned)
                    .unwrap_or_else(|| info.get_port().to_string()),
                tls: info.get_property_val_str("tls") == Some("true"),
                version: info.get_property_val_str("v").unwrap_or("").into(),
            });
        }
    }
    distinct_addresses(found)
}
fn distinct_addresses(all: Vec<Server>) -> Vec<Server> {
    let mut seen = HashMap::new();
    for server in all {
        if !server.host.is_empty() {
            seen.insert(host_port(&server.host, &server.port), server);
        }
    }
    let mut out: Vec<Server> = seen.into_values().collect();
    out.sort_by(|a, b| a.name.cmp(&b.name).then(a.host.cmp(&b.host)));
    out
}
#[derive(Clone)]
pub struct PortSpec {
    pub port: u16,
    pub tls: bool,
}
pub fn standard_ports() -> Vec<PortSpec> {
    vec![
        PortSpec {
            port: 8000,
            tls: false,
        },
        PortSpec {
            port: 8443,
            tls: true,
        },
    ]
}
pub async fn sweep_hosts(hosts: Vec<String>, ports: Vec<PortSpec>) -> Vec<Server> {
    let semaphore = Arc::new(tokio::sync::Semaphore::new(256));
    let mut tasks = tokio::task::JoinSet::new();
    for host in hosts.into_iter().take(1024) {
        for spec in &ports {
            let Ok(permit) = semaphore.clone().acquire_owned().await else {
                return vec![];
            };
            let host = host.clone();
            let spec = spec.clone();
            tasks.spawn(async move {
                let _permit = permit;
                let base = format!(
                    "{}://{}",
                    if spec.tls { "https" } else { "http" },
                    host_port(&host, &spec.port.to_string())
                );
                let remote = Remote::new(&base, "", true)
                    .ok()?
                    .with_timeout(Duration::from_millis(300))
                    .with_response_limit(4096);
                let info = remote.probe().await.ok()?;
                Some(Server {
                    name: if info.name.is_empty() {
                        host.clone()
                    } else {
                        info.name
                    },
                    host,
                    port: spec.port.to_string(),
                    tls: spec.tls,
                    version: info.version,
                })
            });
        }
    }
    let mut all = Vec::new();
    while let Some(result) = tasks.join_next().await {
        if let Ok(Some(server)) = result {
            all.push(server);
        }
    }
    distinct_addresses(all)
}
struct CacheState {
    found: Vec<Server>,
    at: Option<Instant>,
    running: bool,
    closed: bool,
    task: Option<tokio::task::JoinHandle<()>>,
    browses: Vec<Weak<BrowseLifetime>>,
}
#[derive(Clone)]
pub struct SweepCache {
    state: Arc<Mutex<CacheState>>,
}
impl Default for SweepCache {
    fn default() -> Self {
        Self::new()
    }
}
impl SweepCache {
    pub async fn shutdown(&self) {
        let (task, browses) = {
            let mut state = self.state.lock().unwrap_or_else(|e| e.into_inner());
            state.closed = true;
            state.running = false;
            (state.task.take(), std::mem::take(&mut state.browses))
        };
        for browse in browses.into_iter().filter_map(|browse| browse.upgrade()) {
            browse.stop();
        }
        if let Some(task) = task {
            task.abort();
            let _ = task.await;
        }
    }
    pub fn new() -> Self {
        Self {
            state: Arc::new(Mutex::new(CacheState {
                found: vec![],
                at: None,
                running: false,
                closed: false,
                task: None,
                browses: vec![],
            })),
        }
    }
    pub fn get_or_start(&self, hosts: Vec<String>, ports: Vec<PortSpec>) -> Vec<Server> {
        let mut state = self.state.lock().unwrap_or_else(|e| e.into_inner());
        if !state.closed
            && !state.running
            && state
                .at
                .is_none_or(|at| at.elapsed() >= Duration::from_secs(10))
        {
            state.running = true;
            let shared = self.state.clone();
            state.task = Some(tokio::spawn(async move {
                let found =
                    tokio::time::timeout(Duration::from_secs(15), sweep_hosts(hosts, ports))
                        .await
                        .unwrap_or_default();
                let mut state = shared.lock().unwrap_or_else(|e| e.into_inner());
                state.found = found;
                state.at = Some(Instant::now());
                state.running = false;
            }));
        }
        state.found.clone()
    }
    pub async fn servers(&self) -> Vec<Server> {
        let nets = local_networks();
        let swept = self.get_or_start(sweep_targets(&nets), standard_ports());
        let browser = {
            let mut state = self.state.lock().unwrap_or_else(|e| e.into_inner());
            if state.closed {
                return vec![];
            }
            state.browses.retain(|browse| browse.strong_count() > 0);
            BrowseLifetime::start().map(|(session, entries)| {
                state.browses.push(Arc::downgrade(&session));
                (session, entries)
            })
        };
        let mut announced = match browser {
            Ok((session, entries)) => receive_browse(session, entries, Duration::from_millis(1500))
                .await
                .unwrap_or_default(),
            Err(_) => vec![],
        };
        announced.extend(swept);
        collapse(announced, &nets)
    }
}
