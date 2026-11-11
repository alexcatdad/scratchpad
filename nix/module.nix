self:
{ config, lib, pkgs, ... }:

let
  cfg = config.services.scratchpad;
in
{
  # The server embeds the dashboard; there is no renderer process to configure.
  imports = [
    (lib.mkRemovedOptionModule [ "services" "scratchpad" "dashboard" ]
      "The dashboard is embedded in scratchpad-server; the scratchpad-dashboard unit no longer exists.")
    (lib.mkRemovedOptionModule [ "services" "scratchpad" "dashboardUrl" ]
      "The dashboard is embedded in scratchpad-server. SCRATCHPAD_DASHBOARD_URL remains a development-only setting.")
  ];

  options.services.scratchpad = {
    enable = lib.mkEnableOption "the Scratchpad server (API and embedded dashboard)";

    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.scratchpad-server;
      description = "The scratchpad-server package to run.";
    };

    listenAddress = lib.mkOption {
      type = lib.types.str;
      default = "127.0.0.1:3000";
      description = "Address the application listens on.";
    };

    publicUrl = lib.mkOption {
      type = lib.types.str;
      example = "https://memory.example.com";
      description = "Stable public origin. Use HTTPS outside loopback.";
    };

    trustedProxies = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      description = "Reverse proxy addresses trusted for forwarded headers.";
    };

    ragEnabled = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Enable retrieval-augmented generation.";
    };

    encryptionKeyFile = lib.mkOption {
      type = lib.types.path;
      description = ''
        Existing installation key, outside the Nix store (for example an
        agenix or sops-nix path). Delivered to the service as a credential.
      '';
    };

    github.clientId = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      description = "GitHub OAuth app client ID. Set together with the secret in environmentFile.";
    };

    environmentFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      description = ''
        File with KEY=value lines for secrets, such as
        SCRATCHPAD_GITHUB_CLIENT_SECRET or SCRATCHPAD_DATABASE_URL.
        Keep it outside the Nix store.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    # A static user keeps /var/lib/scratchpad a real directory; DynamicUser makes
    # it a symlink, which the server rejects as unsafe storage.
    users.users.scratchpad = {
      isSystemUser = true;
      group = "scratchpad";
      home = "/var/lib/scratchpad";
    };
    users.groups.scratchpad = { };

    systemd.services.scratchpad = {
      description = "Scratchpad server (API and embedded dashboard)";
      wantedBy = [ "multi-user.target" ];
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];
      # SSH sign-in proofs are verified with `ssh-keygen -Y verify`.
      path = [ pkgs.openssh ];

      environment = {
        SCRATCHPAD_LISTEN_ADDR = cfg.listenAddress;
        SCRATCHPAD_PUBLIC_URL = cfg.publicUrl;
        SCRATCHPAD_DATABASE_PATH = "/var/lib/scratchpad/scratchpad.sqlite";
        SCRATCHPAD_ENCRYPTION_KEY_FILE = "%d/installation-key";
        SCRATCHPAD_TRUSTED_PROXIES = lib.concatStringsSep "," cfg.trustedProxies;
        SCRATCHPAD_RAG_ENABLED = lib.boolToString cfg.ragEnabled;
      }
      // lib.optionalAttrs (cfg.github.clientId != null) {
        SCRATCHPAD_GITHUB_CLIENT_ID = cfg.github.clientId;
      };

      serviceConfig = {
        ExecStart = "${lib.getExe cfg.package} serve";
        LoadCredential = "installation-key:${toString cfg.encryptionKeyFile}";
        EnvironmentFile = lib.mkIf (cfg.environmentFile != null) cfg.environmentFile;
        Restart = "on-failure";

        User = "scratchpad";
        Group = "scratchpad";
        StateDirectory = "scratchpad";
        StateDirectoryMode = "0700";
        UMask = "0077";

        NoNewPrivileges = true;
        PrivateTmp = true;
        PrivateDevices = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectControlGroups = true;
        RestrictAddressFamilies = [ "AF_INET" "AF_INET6" "AF_UNIX" ];
        RestrictNamespaces = true;
        LockPersonality = true;
        SystemCallArchitectures = "native";
        CapabilityBoundingSet = "";
      };
    };
  };
}
