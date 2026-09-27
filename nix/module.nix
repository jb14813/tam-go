# NixOS services for Ticket Auction Manager.
#
#   services.tam-server.enable = true;  # the machine that holds an event's data
#   services.tam-client.enable = true;  # a client computer: the web app on http://localhost:3080/
#
# Both run as their own unprivileged users, keep their data in
# /var/lib/tam-server and /var/lib/tam-client, and log to the journal.
flake:
{
  config,
  lib,
  pkgs,
  ...
}:

let
  inherit (lib)
    mkEnableOption
    mkIf
    mkMerge
    mkOption
    optional
    optionals
    types
    ;

  server = config.services.tam-server;
  client = config.services.tam-client;

  defaultPackage = flake.packages.${pkgs.stdenv.hostPlatform.system}.default;
  packageOption = mkOption {
    type = types.package;
    default = defaultPackage;
    defaultText = lib.literalExpression "tam-go.packages.\${system}.default";
    description = "The package with tam-server and tam-client.";
  };

  # The systemd sandbox both services run in.
  hardening = port: {
    DynamicUser = true;
    UMask = "0077";
    Restart = "on-failure";
    AmbientCapabilities = optional (port < 1024) "CAP_NET_BIND_SERVICE";
    CapabilityBoundingSet = optional (port < 1024) "CAP_NET_BIND_SERVICE";
    NoNewPrivileges = true;
    PrivateDevices = true;
    PrivateTmp = true;
    ProtectClock = true;
    ProtectControlGroups = true;
    ProtectHome = true;
    ProtectHostname = true;
    ProtectKernelLogs = true;
    ProtectKernelModules = true;
    ProtectKernelTunables = true;
    ProtectProc = "invisible";
    ProtectSystem = "strict";
    # Netlink lists the network interfaces, for the addresses in the log and
    # for mDNS.
    RestrictAddressFamilies = [
      "AF_INET"
      "AF_INET6"
      "AF_NETLINK"
      "AF_UNIX"
    ];
    RestrictNamespaces = true;
    RestrictRealtime = true;
    RestrictSUIDSGID = true;
    LockPersonality = true;
    MemoryDenyWriteExecute = true;
    SystemCallArchitectures = "native";
    SystemCallFilter = [
      "@system-service"
      "~@privileged"
    ];
  };

  serverArgs = [
    (lib.getExe' server.package "tam-server")
    "-addr"
    "${server.address}:${toString server.port}"
    "-announce=${lib.boolToString server.announce}"
  ]
  ++ optional server.tls "-tls";

  clientURL = "http://localhost:${toString client.port}/";
  # Opens the web app of the running service in the default browser.
  clientDesktopItem = pkgs.makeDesktopItem {
    name = "tam-client";
    desktopName = "Ticket Auction Manager";
    comment = "Tickets, baskets, drawing and reports";
    exec = "${lib.getExe' pkgs.xdg-utils "xdg-open"} ${clientURL}";
    icon = "${client.package}/share/icons/hicolor/scalable/apps/tam-client.svg";
    categories = [ "Office" ];
  };
in
{
  options.services.tam-server = {
    enable = mkEnableOption "tam-server, the shared database of an event's clients";
    package = packageOption;

    address = mkOption {
      type = types.str;
      default = "";
      example = "192.168.1.10";
      description = "The address to listen on; every address when empty. An IPv6 address goes in brackets.";
    };

    port = mkOption {
      type = types.port;
      default = if server.tls then 8443 else 8000;
      defaultText = lib.literalExpression "if tls then 8443 else 8000";
      description = "The port to listen on.";
    };

    tls = mkOption {
      type = types.bool;
      default = false;
      description = ''
        Serve HTTPS. Without certFile and keyFile, the server creates a
        self-signed certificate in its data directory, which a client pins
        when it pairs.
      '';
    };

    certFile = mkOption {
      type = types.nullOr types.path;
      default = null;
      description = "A TLS certificate to serve instead of the self-signed one; needs keyFile.";
    };

    keyFile = mkOption {
      type = types.nullOr types.path;
      default = null;
      description = "The key of certFile.";
    };

    announce = mkOption {
      type = types.bool;
      default = true;
      description = "Announce the server on the local network (mDNS), so clients list it in their Settings.";
    };

    passwordFile = mkOption {
      type = types.nullOr types.path;
      default = null;
      example = "/run/secrets/tam-password";
      description = ''
        A file holding the password that pairs a client and opens the admin
        page (it becomes TAM_PWD). Without it, the password is set on the first
        visit of /admin. Once the password is changed on the admin page, the
        one kept in the data directory wins over this file.
      '';
    };

    openFirewall = mkOption {
      type = types.bool;
      default = false;
      description = "Open the port in the firewall, and UDP 5353 for the announcement.";
    };
  };

  options.services.tam-client = {
    enable = mkEnableOption "tam-client, the web app with its own copy of the data";
    package = packageOption;

    port = mkOption {
      type = types.port;
      default = 3080;
      description = "The port of the web app. It listens on localhost only.";
    };

    openBrowserAtLogin = mkOption {
      type = types.bool;
      default = false;
      description = ''
        Open the web app in the default browser when someone logs into the
        desktop. With automatic login this makes the computer a kiosk.
      '';
    };

    openFirewall = mkOption {
      type = types.bool;
      default = false;
      description = ''
        Open UDP 5353, so the Settings page finds servers by their mDNS
        announcement. Without it the page still finds them by asking the
        addresses of the local network.
      '';
    };
  };

  config = mkMerge [
    (mkIf server.enable {
      assertions = [
        {
          assertion = (server.certFile == null) == (server.keyFile == null);
          message = "services.tam-server: certFile and keyFile go together.";
        }
        {
          assertion = server.certFile == null || server.tls;
          message = "services.tam-server: certFile needs tls = true.";
        }
      ];

      systemd.services.tam-server = {
        description = "Ticket Auction Manager server";
        wantedBy = [ "multi-user.target" ];
        wants = [ "network-online.target" ];
        after = [ "network-online.target" ];
        environment.TAM_DATA_DIR = "/var/lib/tam-server";
        # systemd passes the password, certificate and key as credentials
        # (LoadCredential), readable by the service only.
        script = ''
          if [ -f "''${CREDENTIALS_DIRECTORY:-}/password" ]; then
            TAM_PWD=$(< "$CREDENTIALS_DIRECTORY/password")
            export TAM_PWD
          fi
          if [ -f "''${CREDENTIALS_DIRECTORY:-}/cert" ]; then
            set -- -cert "$CREDENTIALS_DIRECTORY/cert" -key "$CREDENTIALS_DIRECTORY/key"
          fi
          exec ${lib.escapeShellArgs serverArgs} "$@"
        '';
        serviceConfig = hardening server.port // {
          StateDirectory = "tam-server";
          WorkingDirectory = "/var/lib/tam-server";
          LoadCredential =
            optional (server.passwordFile != null) "password:${server.passwordFile}"
            ++ optionals (server.certFile != null) [
              "cert:${server.certFile}"
              "key:${server.keyFile}"
            ];
        };
      };

      networking.firewall = mkIf server.openFirewall {
        allowedTCPPorts = [ server.port ];
        allowedUDPPorts = optional server.announce 5353;
      };
    })

    (mkIf client.enable {
      systemd.services.tam-client = {
        description = "Ticket Auction Manager client (the web app on ${clientURL})";
        wantedBy = [ "multi-user.target" ];
        wants = [ "network-online.target" ];
        after = [ "network-online.target" ];
        environment.TAM_DATA_DIR = "/var/lib/tam-client";
        # Shut Down TAM in the web app ends it cleanly; it stays down until
        # the next boot or `systemctl start tam-client`.
        serviceConfig = hardening client.port // {
          ExecStart = lib.escapeShellArgs [
            (lib.getExe' client.package "tam-client")
            "-addr"
            "localhost:${toString client.port}"
            "-open=false"
          ];
          StateDirectory = "tam-client";
          WorkingDirectory = "/var/lib/tam-client";
        };
      };

      environment.systemPackages = [ clientDesktopItem ];
      environment.etc."xdg/autostart/tam-client.desktop" = mkIf client.openBrowserAtLogin {
        source = "${clientDesktopItem}/share/applications/tam-client.desktop";
      };

      networking.firewall.allowedUDPPorts = mkIf client.openFirewall [ 5353 ];
    })
  ];
}
