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

	"github.com/barrikadelabs/barrikade-lens/internal/catalog"
	"github.com/barrikadelabs/barrikade-lens/internal/cloud"
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
	webDatabaseURL := flag.String("web-database-url", os.Getenv("LENS_WEB_DATABASE_URL"), "optional PostgreSQL URL for the RLS-constrained web role")
	workerDatabaseURL := flag.String("worker-database-url", os.Getenv("LENS_WORKER_DATABASE_URL"), "optional PostgreSQL URL for a login granted the lens_worker role")
	publicURL := flag.String("public-url", env("LENS_PUBLIC_URL", "http://localhost:8080"), "public Hub base URL")
	organizationID := flag.String("organization", env("LENS_ORGANIZATION_ID", "default"), "default organization ID")
	organizationName := flag.String("organization-name", env("LENS_ORGANIZATION_NAME", "Lens Organization"), "default organization name")
	devAdminToken := flag.String("dev-admin-token", os.Getenv("LENS_DEV_ADMIN_TOKEN"), "local bootstrap administrator token")
	jwtSecret := flag.String("jwt-secret", os.Getenv("LENS_JWT_SECRET"), "collector JWT signing secret (at least 32 bytes)")
	migrateOnly := flag.Bool("migrate-only", false, "apply database migrations and exit")
	catalogEnabled := flag.Bool("catalog-enabled", env("LENS_CATALOG_ENABLED", "true") != "false", "enable Hub-only public catalog enrichment")
	catalogManifest := flag.String("catalog-manifest", env("LENS_CATALOG_MANIFEST", catalog.PublicCatalogManifest), "OAK-compatible compact catalog manifest")
	exposureEnabled := flag.Bool("exposure-enabled", env("LENS_EXPOSURE_ENABLED", "false") == "true", "enable evidence-backed exposure maps and findings")
	uiDir := flag.String("ui-dir", os.Getenv("LENS_UI_DIR"), "directory containing the built Lens Hub UI")
	authMode := flag.String("auth-mode", env("LENS_AUTH_MODE", "development"), "authentication mode: clerk, oidc, or development")
	oidcIssuer := flag.String("oidc-issuer", os.Getenv("LENS_OIDC_ISSUER"), "OIDC issuer URL")
	oidcClientID := flag.String("oidc-client-id", os.Getenv("LENS_OIDC_CLIENT_ID"), "OIDC client ID")
	oidcClientSecret := flag.String("oidc-client-secret", os.Getenv("LENS_OIDC_CLIENT_SECRET"), "OIDC client secret, if required")
	oidcRedirectURI := flag.String("oidc-redirect-uri", os.Getenv("LENS_OIDC_REDIRECT_URI"), "OIDC browser redirect URI")
	oidcAdminGroup := flag.String("oidc-admin-group", os.Getenv("LENS_OIDC_ADMIN_GROUP"), "OIDC group granted Lens administration")
	clerkIssuer := flag.String("clerk-issuer", os.Getenv("LENS_CLERK_ISSUER"), "Clerk Frontend API issuer URL")
	clerkPublishableKey := flag.String("clerk-publishable-key", os.Getenv("LENS_CLERK_PUBLISHABLE_KEY"), "Clerk publishable key exposed to the browser")
	clerkAuthorizedParty := flag.String("clerk-authorized-party", os.Getenv("LENS_CLERK_AUTHORIZED_PARTY"), "allowed Clerk session token azp origin")
	clerkWebhookSecret := flag.String("clerk-webhook-secret", os.Getenv("LENS_CLERK_WEBHOOK_SECRET"), "Clerk webhook signing secret")
	clerkSecretKey := flag.String("clerk-secret-key", os.Getenv("LENS_CLERK_SECRET_KEY"), "Clerk backend secret for Lens-governed identity deletion")
	selfServeEnabled := flag.Bool("self-serve-enabled", env("LENS_SELF_SERVE_ENABLED", "false") == "true", "enable self-serve environment onboarding")
	awsConnectorEnabled := flag.Bool("aws-connector-enabled", env("LENS_AWS_CONNECTOR_ENABLED", "false") == "true", "enable the AWS cloud connector")
	azureConnectorEnabled := flag.Bool("azure-connector-enabled", env("LENS_AZURE_CONNECTOR_ENABLED", "false") == "true", "enable the Azure cloud connector")
	gcpConnectorEnabled := flag.Bool("gcp-connector-enabled", env("LENS_GCP_CONNECTOR_ENABLED", "false") == "true", "enable the GCP cloud connector")
	endpointConnectorEnabled := flag.Bool("endpoint-connector-enabled", env("LENS_ENDPOINT_CONNECTOR_ENABLED", "false") == "true", "enable endpoint self-service onboarding")
	kubernetesConnectorEnabled := flag.Bool("kubernetes-connector-enabled", env("LENS_KUBERNETES_CONNECTOR_ENABLED", "false") == "true", "enable Kubernetes self-service onboarding")
	githubConnectorEnabled := flag.Bool("github-connector-enabled", env("LENS_GITHUB_CONNECTOR_ENABLED", "false") == "true", "enable GitHub repository onboarding")
	endpointHandoffEnabled := flag.Bool("endpoint-handoff-enabled", env("LENS_ENDPOINT_HANDOFF_ENABLED", "false") == "true", "enable delegated endpoint setup")
	githubAppID := flag.String("github-app-id", os.Getenv("LENS_GITHUB_APP_ID"), "GitHub App ID for repository discovery")
	githubPrivateKeyFile := flag.String("github-private-key-file", os.Getenv("LENS_GITHUB_PRIVATE_KEY_FILE"), "GitHub App private key PEM file")
	githubWebhookSecret := flag.String("github-webhook-secret", os.Getenv("LENS_GITHUB_WEBHOOK_SECRET"), "GitHub App webhook signing secret")
	githubAppSlug := flag.String("github-app-slug", os.Getenv("LENS_GITHUB_APP_SLUG"), "GitHub App slug used to launch installation")
	awsBrokerRoleARN := flag.String("aws-broker-role-arn", os.Getenv("LENS_AWS_BROKER_ROLE_ARN"), "Lens-owned AWS broker role trusted by customer roles")
	azureApplicationID := flag.String("azure-application-id", os.Getenv("LENS_AZURE_APPLICATION_ID"), "multitenant Lens Entra application client ID")
	gcpWorkloadIssuer := flag.String("gcp-workload-issuer", os.Getenv("LENS_GCP_WORKLOAD_ISSUER"), "Azure workload token issuer accepted by GCP")
	gcpWorkloadAudience := flag.String("gcp-workload-audience", os.Getenv("LENS_GCP_WORKLOAD_AUDIENCE"), "GCP workload identity provider audience")
	managedIdentityClientID := flag.String("managed-identity-client-id", os.Getenv("LENS_AZURE_MANAGED_IDENTITY_CLIENT_ID"), "Azure user-assigned managed identity client ID")
	managedIdentityPrincipalID := flag.String("managed-identity-principal-id", os.Getenv("LENS_AZURE_MANAGED_IDENTITY_PRINCIPAL_ID"), "Azure user-assigned managed identity principal/object ID")
	awsFederationAudience := flag.String("aws-federation-audience", os.Getenv("LENS_AWS_FEDERATION_AUDIENCE"), "audience used for Azure-to-AWS workload federation")
	azureAssertionAudience := flag.String("azure-assertion-audience", env("LENS_AZURE_ASSERTION_AUDIENCE", "api://AzureADTokenExchange"), "audience for the Azure managed identity client assertion")
	gcpAssertionAudience := flag.String("gcp-assertion-audience", os.Getenv("LENS_GCP_ASSERTION_AUDIENCE"), "audience for the Azure token exchanged with GCP")
	awsRegions := flag.String("aws-regions", os.Getenv("LENS_AWS_REGIONS"), "optional comma-separated commercial AWS regions")
	flag.Parse()
	if *databaseURL == "" {
		return fmt.Errorf("--database-url or LENS_DATABASE_URL is required")
	}
	if len(*jwtSecret) < 32 {
		return fmt.Errorf("--jwt-secret or LENS_JWT_SECRET must contain at least 32 bytes")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	adminPool, err := hub.Open(ctx, *databaseURL)
	if err != nil {
		return err
	}
	defer adminPool.Close()
	if err := hub.Migrate(ctx, adminPool); err != nil {
		return err
	}
	if *migrateOnly {
		return nil
	}
	webPool := adminPool
	if *webDatabaseURL != "" && *webDatabaseURL != *databaseURL {
		webPool, err = hub.Open(ctx, *webDatabaseURL)
		if err != nil {
			return fmt.Errorf("open web database pool: %w", err)
		}
		defer webPool.Close()
	}
	workerPool := adminPool
	if *workerDatabaseURL != "" && *workerDatabaseURL != *databaseURL {
		workerPool, err = hub.Open(ctx, *workerDatabaseURL)
		if err != nil {
			return fmt.Errorf("open worker database pool: %w", err)
		}
		defer workerPool.Close()
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
	cloudAdapters := cloud.Registry{}
	managedIdentity := cloud.ManagedIdentityTokenSource{ClientID: *managedIdentityClientID}
	if *awsConnectorEnabled {
		if *awsBrokerRoleARN == "" || *awsFederationAudience == "" {
			return fmt.Errorf("AWS connector requires LENS_AWS_BROKER_ROLE_ARN and LENS_AWS_FEDERATION_AUDIENCE")
		}
		regions := []string{}
		for _, region := range strings.Split(*awsRegions, ",") {
			if region = strings.TrimSpace(region); region != "" {
				regions = append(regions, region)
			}
		}
		cloudAdapters["aws"] = cloud.NewAWSAdapter(cloud.AWSFederatedBroker{TokenSource: managedIdentity, BrokerRoleARN: *awsBrokerRoleARN, Audience: *awsFederationAudience}, nil, regions)
	}
	if *azureConnectorEnabled {
		if *azureApplicationID == "" {
			return fmt.Errorf("Azure connector requires LENS_AZURE_APPLICATION_ID")
		}
		cloudAdapters["azure"] = cloud.NewAzureAdapter(cloud.AzureFederatedBroker{TokenSource: managedIdentity, ApplicationID: *azureApplicationID, AssertionAudience: *azureAssertionAudience}, nil)
	}
	if *gcpConnectorEnabled {
		if *gcpWorkloadIssuer == "" || *gcpAssertionAudience == "" || *managedIdentityPrincipalID == "" {
			return fmt.Errorf("GCP connector requires workload issuer, assertion audience, and managed identity principal ID")
		}
		cloudAdapters["gcp"] = cloud.NewGCPAdapter(cloud.GCPFederatedBroker{TokenSource: managedIdentity, Audience: *gcpWorkloadAudience, AssertionAudience: *gcpAssertionAudience}, nil)
	}
	server, err := hub.NewServer(ctx, hub.Config{
		Pool: webPool, WorkerPool: workerPool, JWTSecret: []byte(*jwtSecret), AuthMode: *authMode, DevAdminToken: *devAdminToken,
		DefaultOrganizationID: *organizationID, DefaultOrganizationName: *organizationName,
		PublicURL: *publicURL, Logger: slog.Default(), UIDir: *uiDir,
		OIDCIssuer: *oidcIssuer, OIDCClientID: *oidcClientID, OIDCClientSecret: *oidcClientSecret,
		OIDCRedirectURI: *oidcRedirectURI, OIDCAdminGroup: *oidcAdminGroup,
		ClerkIssuer: *clerkIssuer, ClerkPublishableKey: *clerkPublishableKey,
		ClerkAuthorizedParty: *clerkAuthorizedParty, ClerkWebhookSecret: *clerkWebhookSecret, ClerkSecretKey: *clerkSecretKey,
		GitHubWebhookSecret: []byte(*githubWebhookSecret), GitHubClient: githubClient, GitHubAppSlug: *githubAppSlug,
		ExposureEnabled: *exposureEnabled, SelfServeEnabled: *selfServeEnabled,
		AWSConnectorEnabled: *awsConnectorEnabled, AzureConnectorEnabled: *azureConnectorEnabled,
		GCPConnectorEnabled: *gcpConnectorEnabled, EndpointConnectorEnabled: *endpointConnectorEnabled,
		KubernetesConnectorEnabled: *kubernetesConnectorEnabled, GitHubConnectorEnabled: *githubConnectorEnabled,
		EndpointHandoffEnabled: *endpointHandoffEnabled,
		AWSBrokerRoleARN:       *awsBrokerRoleARN, AzureApplicationID: *azureApplicationID,
		GCPWorkloadIssuer: *gcpWorkloadIssuer, GCPWorkloadAudience: *gcpWorkloadAudience,
		GCPAssertionAudience:       *gcpAssertionAudience,
		ManagedIdentityPrincipalID: *managedIdentityPrincipalID,
		CloudAdapters:              cloudAdapters,
	})
	if err != nil {
		return err
	}
	httpServer := &http.Server{Addr: *listen, Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 35 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	errChannel := make(chan error, 5)
	go func() { errChannel <- hub.Worker{Pool: workerPool, Logger: slog.Default()}.Run(ctx) }()
	go func() { errChannel <- hub.WebhookWorker{Pool: workerPool, Logger: slog.Default()}.Run(ctx) }()
	if *selfServeEnabled {
		go func() {
			errChannel <- hub.CloudWorker{Pool: workerPool, Adapters: cloudAdapters, Logger: slog.Default()}.Run(ctx)
		}()
	}
	if *catalogEnabled {
		provider := &catalog.OAKProvider{ProviderID: "public-api-catalog", Name: "Public API Catalog", ManifestURL: *catalogManifest}
		go func() {
			errChannel <- (&hub.CatalogWorker{Pool: workerPool, Provider: provider, Logger: slog.Default()}).Run(ctx)
		}()
	}
	if *exposureEnabled {
		go func() { errChannel <- (hub.ExposureWorker{Pool: workerPool, Logger: slog.Default()}).Run(ctx) }()
	}
	if githubClient != nil {
		go func() {
			errChannel <- hub.RepositoryWorker{Pool: workerPool, Client: githubClient, Logger: slog.Default()}.Run(ctx)
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
