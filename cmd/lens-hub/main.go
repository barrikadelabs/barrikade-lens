package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/barrikadelabs/barrikade-lens/internal/ard"
	"github.com/barrikadelabs/barrikade-lens/internal/catalog"
	"github.com/barrikadelabs/barrikade-lens/internal/githubapp"
	"github.com/barrikadelabs/barrikade-lens/internal/hub"
)

var version = "2.0.0-dev"

func main() {
	if err := run(); err != nil {
		slog.Error("Lens Hub stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	listen := flag.String("listen", env("LENS_LISTEN", ":8080"), "HTTP listen address")
	databaseURL := flag.String("database-url", os.Getenv("LENS_DATABASE_URL"), "PostgreSQL connection URL")
	publicURL := flag.String("public-url", env("LENS_PUBLIC_URL", "http://localhost:8080"), "public Hub base URL")
	organizationID := flag.String("organization", env("LENS_ORGANIZATION_ID", "default"), "default organization ID")
	organizationName := flag.String("organization-name", env("LENS_ORGANIZATION_NAME", "Lens Organization"), "default organization name")
	devAdminToken := flag.String("dev-admin-token", os.Getenv("LENS_DEV_ADMIN_TOKEN"), "local bootstrap administrator token")
	jwtSecret := flag.String("jwt-secret", os.Getenv("LENS_JWT_SECRET"), "collector JWT signing secret (at least 32 bytes)")
	migrateOnly := flag.Bool("migrate-only", false, "apply database migrations and exit")
	catalogEnabled := flag.Bool("catalog-enabled", env("LENS_CATALOG_ENABLED", "true") != "false", "enable Hub-only public catalog enrichment")
	catalogManifest := flag.String("catalog-manifest", env("LENS_CATALOG_MANIFEST", catalog.PublicCatalogManifest), "OAK-compatible compact catalog manifest")
	ardEnabled := flag.Bool("ard-enabled", env("LENS_ARD_ENABLED", "true") != "false", "enable explicitly configured ARD catalog discovery")
	ardPrivateHosts := flag.String("ard-private-host-allowlist", os.Getenv("LENS_ARD_PRIVATE_HOST_ALLOWLIST"), "comma-separated exact private ARD catalog hosts")
	uiDir := flag.String("ui-dir", os.Getenv("LENS_UI_DIR"), "directory containing the built Lens Hub UI")
	oidcIssuer := flag.String("oidc-issuer", os.Getenv("LENS_OIDC_ISSUER"), "OIDC issuer URL")
	oidcClientID := flag.String("oidc-client-id", os.Getenv("LENS_OIDC_CLIENT_ID"), "OIDC client ID")
	oidcClientSecret := flag.String("oidc-client-secret", os.Getenv("LENS_OIDC_CLIENT_SECRET"), "OIDC client secret, if required")
	oidcRedirectURI := flag.String("oidc-redirect-uri", os.Getenv("LENS_OIDC_REDIRECT_URI"), "OIDC browser redirect URI")
	oidcAdminGroup := flag.String("oidc-admin-group", os.Getenv("LENS_OIDC_ADMIN_GROUP"), "OIDC group granted Lens administration")
	githubAppID := flag.String("github-app-id", os.Getenv("LENS_GITHUB_APP_ID"), "GitHub App ID for repository discovery")
	githubPrivateKeyFile := flag.String("github-private-key-file", os.Getenv("LENS_GITHUB_PRIVATE_KEY_FILE"), "GitHub App private key PEM file")
	githubWebhookSecret := flag.String("github-webhook-secret", os.Getenv("LENS_GITHUB_WEBHOOK_SECRET"), "GitHub App webhook signing secret")
	flag.Parse()
	if *databaseURL == "" {
		return fmt.Errorf("--database-url or LENS_DATABASE_URL is required")
	}
	if len(*jwtSecret) < 32 {
		return fmt.Errorf("--jwt-secret or LENS_JWT_SECRET must contain at least 32 bytes")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := hub.Open(ctx, *databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := hub.Migrate(ctx, pool); err != nil {
		return err
	}
	if *migrateOnly {
		return nil
	}
	hub.Version = version
	var githubClient *githubapp.Client
	if *githubAppID != "" || *githubPrivateKeyFile != "" || *githubWebhookSecret != "" {
		if *githubAppID == "" || *githubPrivateKeyFile == "" || *githubWebhookSecret == "" {
			return fmt.Errorf("GitHub App ID, private key file, and webhook secret must be configured together")
		}
		key, readErr := os.ReadFile(*githubPrivateKeyFile)
		if readErr != nil {
			return readErr
		}
		githubClient, err = githubapp.New(*githubAppID, key, version)
		if err != nil {
			return err
		}
	}
	allowedARDHosts := map[string]bool{}
	for _, host := range strings.Split(*ardPrivateHosts, ",") {
		if host = strings.ToLower(strings.TrimSpace(host)); host != "" {
			allowedARDHosts[host] = true
		}
	}
	ardProvider := &ard.Provider{AllowedPrivateHosts: allowedARDHosts}
	server, err := hub.NewServer(ctx, hub.Config{Pool: pool, JWTSecret: []byte(*jwtSecret), DevAdminToken: *devAdminToken, DefaultOrganizationID: *organizationID, DefaultOrganizationName: *organizationName, PublicURL: *publicURL, Logger: slog.Default(), UIDir: *uiDir, OIDCIssuer: *oidcIssuer, OIDCClientID: *oidcClientID, OIDCClientSecret: *oidcClientSecret, OIDCRedirectURI: *oidcRedirectURI, OIDCAdminGroup: *oidcAdminGroup, GitHubWebhookSecret: []byte(*githubWebhookSecret), GitHubClient: githubClient, ARDProvider: ardProvider, ARDDisabled: !*ardEnabled})
	if err != nil {
		return err
	}
	httpServer := &http.Server{Addr: *listen, Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 35 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	errChannel := make(chan error, 6)
	go func() { errChannel <- hub.Worker{Pool: pool, Logger: slog.Default()}.Run(ctx) }()
	go func() { errChannel <- hub.WebhookWorker{Pool: pool, Logger: slog.Default()}.Run(ctx) }()
	if *catalogEnabled {
		provider := &catalog.OAKProvider{ProviderID: "public-api-catalog", Name: "Public API Catalog", ManifestURL: *catalogManifest}
		go func() {
			errChannel <- (&hub.CatalogWorker{Pool: pool, Provider: provider, Logger: slog.Default()}).Run(ctx)
		}()
	}
	if *ardEnabled {
		go func() {
			errChannel <- (hub.ARDWorker{Pool: pool, Provider: ardProvider, Logger: slog.Default()}).Run(ctx)
		}()
	}
	if githubClient != nil {
		go func() {
			errChannel <- hub.RepositoryWorker{Pool: pool, Client: githubClient, Logger: slog.Default()}.Run(ctx)
		}()
	}
	go func() {
		slog.Info("Lens Hub listening", "address", *listen, "version", version)
		errChannel <- httpServer.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errChannel:
		if errors.Is(err, http.ErrServerClosed) || errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
