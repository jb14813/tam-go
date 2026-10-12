use r_tam_core::cli::{Options, Role, listen_address};
#[test]
fn go_help_stops_parsing_at_the_help_flag() {
    for role in [Role::Client, Role::Server] {
        for flag in ["-h", "--help", "-help", "--h", "-h=false"] {
            assert!(
                Options::parse(role, [flag, "-unknown"]).unwrap().help,
                "{role:?}: {flag}"
            );
        }
        assert!(Options::parse(role, ["-unknown", "-h"]).is_err());
        assert!(Options::parse(role, ["-tray=invalid", "-h"]).is_err());
        assert_eq!(Options::parse(role, ["-addr", "-h"]).unwrap().addr, "-h");
        assert!(!Options::parse(role, ["positional", "-h"]).unwrap().help);
        assert!(!Options::parse(role, ["--", "-h"]).unwrap().help);
    }
}
#[test]
fn listen_addresses_preserve_explicit_hosts_and_go_defaults() {
    for (input, expected) in [
        (":0", "0.0.0.0:0"),
        (":3080", "0.0.0.0:3080"),
        ("localhost:3080", "localhost:3080"),
        ("127.0.0.1:0", "127.0.0.1:0"),
        ("[::1]:8443", "[::1]:8443"),
        ("[::]:0", "[::]:0"),
        ("example.test:8000", "example.test:8000"),
    ] {
        assert_eq!(listen_address(input), expected);
    }
    for (role, args, expected) in [
        (Role::Client, vec![], "localhost:3080"),
        (Role::Server, vec![], "0.0.0.0:8000"),
        (Role::Server, vec!["-tls"], "0.0.0.0:8443"),
        (Role::Server, vec!["dev"], "localhost:8000"),
        (Role::Server, vec!["-tls", "dev"], "localhost:8443"),
    ] {
        assert_eq!(
            listen_address(&Options::parse(role, args).unwrap().addr),
            expected
        );
    }
}
#[test]
fn go_flag_boolean_syntax_defaults_and_dev() {
    let c = Options::parse(
        Role::Client,
        ["-addr", "[::1]:3081", "-open=false", "-tray=0"],
    )
    .unwrap();
    assert_eq!(c.addr, "[::1]:3081");
    assert!(!c.open);
    assert!(!c.tray);
    let s = Options::parse(Role::Server, ["-tls", "-announce=false", "dev"]).unwrap();
    assert_eq!(s.addr, "localhost:8443");
    assert!(s.tls);
    assert!(!s.announce);
    assert_eq!(
        Options::parse(Role::Server, ["dev", "-tls"]).unwrap().addr,
        "localhost:8000"
    );
    assert!(Options::parse(Role::Client, ["-open=perhaps"]).is_err());
    assert!(Options::parse(Role::Client, ["-tls"]).is_err());
    assert_eq!(
        Options::parse(Role::Server, ["-addr=:9000", "dev"])
            .unwrap()
            .addr,
        ":9000"
    );
    assert_eq!(Options::parse(Role::Client, ["-addr="]).unwrap().addr, "");
}
