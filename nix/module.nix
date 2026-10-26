self:
{ config, lib, pkgs, ... }:

let
  cfg = config.services.scratchpad;
  dashboardUrl =
    if cfg.dashboardUrl != null then cfg.dashboardUrl
    else if cfg.dashboard.enable then "http://127.0.0.1:${toString cfg.dashboard.port}"
    else null;
in
{
  options.services.scratchpad = {
    enable = lib.mkEnableOption "the Scratchpad central API server";

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

    dashboardUrl = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      example = "http://127.0.0.1:4000";
      description = "Override the loopback dashboard origin, for an externally run renderer.";
    };

    dashboard = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = "Run the dashboard renderer on loopback and proxy it from the API server.";
      };
      package = lib.mkOption {
        type = lib.types.package;
        default = self.packages.${pkgs.stdenv.hostPlatform.system}.scratchpad-dashboard;
        description = "The dashboard package to run.";
      };
      port = lib.mkOption {
        type = lib.types.port;
        default = 4000;
        description = "Loopback port of the dashboard renderer.";
      };
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
    systemd.services.scratchpad = {
      description = "Scratchpad central API server";
      wantedBy = [ "multi-user.target" ];
      after = [ "network-online.target" ] ++ lib.optional cfg.dashboard.enable "scratchpad-dashboard.service";
      wants = [ "network-online.target" ];
      requires = lib.optional cfg.dashboard.enable "scratchpad-dashboard.service";

      environment = {
        SCRATCHPAD_LISTEN_ADDR = cfg.listenAddress;
        SCRATCHPAD_PUBLIC_URL = cfg.publicUrl;
        SCRATCHPAD_DATABASE_PATH = "/var/lib/scratchpad/scratchpad.sqlite";
        SCRATCHPAD_ENCRYPTION_KEY_FILE = "%d/installation-key";
        SCRATCHPAD_TRUSTED_PROXIES = lib.concatStringsSep "," cfg.trustedProxies;
        SCRATCHPAD_RAG_ENABLED = lib.boolToString cfg.ragEnabled;
      }
      // lib.optionalAttrs (dashboardUrl != null) {
        SCRATCHPAD_DASHBOARD_URL = dashboardUrl;
      }
      // lib.optionalAttrs (cfg.github.clientId != null) {
        SCRATCHPAD_GITHUB_CLIENT_ID = cfg.github.clientId;
      };

      serviceConfig = {
        ExecStart = "${lib.getExe cfg.package} serve";
        LoadCredential = "installation-key:${toString cfg.encryptionKeyFile}";
        EnvironmentFile = lib.mkIf (cfg.environmentFile != null) cfg.environmentFile;
        Restart = "on-failure";

        DynamicUser = true;
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

    systemd.services.scratchpad-dashboard = lib.mkIf cfg.dashboard.enable {
      description = "Scratchpad dashboard renderer";
      wantedBy = [ "multi-user.target" ];

      # The renderer holds no credentials; the API server owns all data and auth.
      environment = {
        NODE_ENV = "production";
        HOST = "127.0.0.1";
        PORT = toString cfg.dashboard.port;
        SCRATCHPAD_UI_ONLY = "true";
      };

      serviceConfig = {
        ExecStart = "${lib.getExe pkgs.nodejs_24} ${cfg.dashboard.package}/apps/web/server.mjs";
        Restart = "on-failure";
        DynamicUser = true;
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
