# A NixOS test of the module: a server and a laptop on one network. The
# laptop finds the server by its announcement, pairs with it the way its
# Settings page does, and saves a prefix and a ticket; the ticket is then
# on the server, also after both services restart. A second server serves
# HTTPS with a certificate of its own.
flake:
{ pkgs, ... }:

let
  password = "nixos-test";

  # A certificate for the second server's name, as an admin would bring one.
  cert = pkgs.runCommand "tam-test-cert" { nativeBuildInputs = [ pkgs.openssl ]; } ''
    mkdir $out
    openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 3650       -subj /CN=secure -addext subjectAltName=DNS:secure       -keyout $out/key.pem -out $out/cert.pem
  '';
in
{
  name = "tam";

  nodes = {
    server = {
      imports = [ flake.nixosModules.default ];
      services.tam-server = {
        enable = true;
        openFirewall = true;
        passwordFile = pkgs.writeText "tam-password" password;
      };
      environment.systemPackages = [ pkgs.curl ];
    };

    secure = {
      imports = [ flake.nixosModules.default ];
      services.tam-server = {
        enable = true;
        openFirewall = true;
        tls = true;
        certFile = "${cert}/cert.pem";
        keyFile = "${cert}/key.pem";
      };
    };

    laptop = {
      imports = [ flake.nixosModules.default ];
      services.tam-client = {
        enable = true;
        openFirewall = true;
        openBrowserAtLogin = true;
      };
      environment.systemPackages = [ pkgs.curl ];
    };
  };

  testScript = ''
    import json

    def post(machine, url, body, headers=""):
        return machine.succeed(
            f"curl -sSf -X POST -H 'Content-Type: application/json' {headers} "
            f"-d '{json.dumps(body)}' {url}"
        )

    start_all()
    server.wait_for_unit("tam-server.service")
    server.wait_for_open_port(8000)
    laptop.wait_for_unit("tam-client.service")
    laptop.wait_for_open_port(3080)

    with subtest("the laptop serves the web app"):
        laptop.succeed("curl -sSf http://localhost:3080/web/ | grep -q '<html'")
        laptop.succeed("grep -q 'xdg-open http://localhost:3080/' /etc/xdg/autostart/tam-client.desktop")

    with subtest("the server answers through its open port"):
        root = json.loads(laptop.succeed("curl -sSf http://server:8000/api"))
        assert root["whoami"] == "TAM Server", root

    with subtest("the laptop finds the server by its announcement"):
        laptop.wait_until_succeeds("curl -sSf http://localhost:3080/api/servers | grep -q '\"name\":\"server\"'", timeout=60)

    with subtest("the laptop pairs and saves through the server"):
        post(laptop, "http://localhost:3080/api/pair", {"host": "server", "port": "8000", "password": "${password}"})
        laptop.wait_until_succeeds("curl -sSf http://localhost:3080/api/status | grep -q '\"state\":\"connected\"'", timeout=30)
        post(laptop, "http://localhost:3080/api/prefixes", [{"prefix": "NIX", "color": "blue", "weight": 1}])
        post(laptop, "http://localhost:3080/api/tickets", [{"prefix": "NIX", "t_id": 1, "first_name": "Ada", "last_name": "Lovelace", "phone_number": "555-0100", "pref": "CALL"}])

    def server_has_the_ticket():
        key = json.loads(post(server, "http://localhost:8000/api/auth", {"description": "test"}, "-H 'TAM-PW: ${password}'"))["auth_key"]
        # The route answers a list, as in the original.
        tickets = json.loads(server.succeed(f"curl -sSf -H 'TAM-KEY: {key}' http://localhost:8000/api/tickets/NIX/1"))
        assert [t["last_name"] for t in tickets] == ["Lovelace"], tickets

    with subtest("the ticket is on the server"):
        server_has_the_ticket()

    with subtest("both keep their data over a restart"):
        server.systemctl("restart tam-server.service")
        server.wait_for_open_port(8000)
        server_has_the_ticket()
        laptop.systemctl("restart tam-client.service")
        laptop.wait_for_open_port(3080)
        laptop.wait_until_succeeds("curl -sSf http://localhost:3080/api/status | grep -q '\"state\":\"connected\"'", timeout=30)
        laptop.succeed("curl -sSf http://localhost:3080/api/tickets/NIX/1 | grep -q Lovelace")

    with subtest("a server with its own certificate serves it"):
        secure.wait_for_unit("tam-server.service")
        secure.wait_for_open_port(8443)
        root = json.loads(laptop.succeed("curl -sSf --cacert ${cert}/cert.pem https://secure:8443/api"))
        assert root["whoami"] == "TAM Server", root

    with subtest("Shut Down TAM stops the client until it is started again"):
        laptop.succeed("curl -sSf -X POST -H 'Content-Type: application/json' -d '{}' http://localhost:3080/api/shutdown")
        laptop.wait_until_succeeds("systemctl show -p ActiveState --value tam-client.service | grep -qx inactive", timeout=30)
        laptop.succeed("systemctl start tam-client.service")
        laptop.wait_for_open_port(3080)
  '';
}
