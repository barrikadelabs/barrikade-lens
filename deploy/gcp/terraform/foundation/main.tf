/*
Enables GCP services, creates the app-image repo,
creates budget alert and preps secure deployments accounts for GitHub.
*/

locals {
  common_labels = merge(var.labels, {
    application  = "barrikade-lens"
    environment  = var.environment
    "managed-by" = "terraform"
  })
  required_services = toset([
    "artifactregistry.googleapis.com",
    "billingbudgets.googleapis.com",
    "cloudbuild.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "compute.googleapis.com",
    "iam.googleapis.com",
    "iamcredentials.googleapis.com",
    "logging.googleapis.com",
    "monitoring.googleapis.com",
    "orgpolicy.googleapis.com",
    "run.googleapis.com",
    "secretmanager.googleapis.com",
    "servicenetworking.googleapis.com",
    "serviceusage.googleapis.com",
    "sqladmin.googleapis.com",
    "sts.googleapis.com",
  ])
}

data "google_project" "environment" {
  project_id = var.project_id
}

resource "google_project_service" "required" {
  for_each = local.required_services

  project            = data.google_project.environment.project_id
  service            = each.value
  disable_on_destroy = false
}

resource "google_project_iam_member" "cloud_build_builder" {
  project = data.google_project.environment.project_id
  role    = "roles/cloudbuild.builds.builder"
  member  = "serviceAccount:${data.google_project.environment.number}-compute@developer.gserviceaccount.com"

  depends_on = [google_project_service.required]
}

resource "google_artifact_registry_repository" "lens" {
  project       = data.google_project.environment.project_id
  location      = var.region
  repository_id = "lens"
  description   = "Immutable Lens Hub images for ${var.environment}"
  format        = "DOCKER"
  mode          = "STANDARD_REPOSITORY"
  labels        = local.common_labels

  docker_config {
    immutable_tags = true
  }

  depends_on = [google_project_service.required]
}

resource "google_service_account" "release" {
  for_each = var.deployment_targets

  project      = data.google_project.environment.project_id
  account_id   = "lens-${each.key}-release"
  display_name = "Lens ${each.key} ${var.environment} release"
  description  = "Keyless GitHub identity isolated to ${each.key} in ${var.environment}"
}

resource "google_artifact_registry_repository_iam_member" "release_writer" {
  for_each = var.deployment_targets

  project    = data.google_project.environment.project_id
  location   = google_artifact_registry_repository.lens.location
  repository = google_artifact_registry_repository.lens.name
  role       = "roles/artifactregistry.writer"
  member     = "serviceAccount:${google_service_account.release[each.key].email}"
}

resource "google_iam_workload_identity_pool" "github" {
  project                   = data.google_project.environment.project_id
  workload_identity_pool_id = "github-${var.environment}"
  display_name              = "GitHub ${var.environment}"
  description               = "Repository and environment restricted GitHub Actions identities"

  depends_on = [google_project_service.required]
}

resource "google_iam_workload_identity_pool_provider" "github" {
  for_each = var.deployment_targets

  project                            = data.google_project.environment.project_id
  workload_identity_pool_id          = google_iam_workload_identity_pool.github.workload_identity_pool_id
  workload_identity_pool_provider_id = "github-${each.key}"
  display_name                       = "GitHub ${each.key}"
  description                        = "Trust ${var.github_repository} environment ${each.value.github_environment}"

  attribute_mapping = {
    "google.subject"       = "assertion.sub"
    "attribute.repository" = "assertion.repository"
  }

  attribute_condition = join(" && ", [
    "assertion.repository_owner_id == '${var.github_owner_id}'",
    "assertion.repository == '${var.github_repository}'",
    "assertion.sub == 'repo:${var.github_repository}:environment:${each.value.github_environment}'",
  ])

  oidc {
    issuer_uri = "https://token.actions.githubusercontent.com"
  }
}

resource "google_service_account_iam_member" "github_release" {
  for_each = var.deployment_targets

  service_account_id = google_service_account.release[each.key].name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.github.name}/attribute.repository/${var.github_repository}"
}

resource "google_billing_budget" "environment" {
  billing_account = var.billing_account
  display_name    = "Barrikade Lens ${var.environment} monthly budget"

  budget_filter {
    projects = ["projects/${data.google_project.environment.number}"]
  }

  amount {
    specified_amount {
      currency_code = "EUR"
      units         = tostring(floor(var.budget_eur))
      nanos         = floor((var.budget_eur - floor(var.budget_eur)) * 1000000000)
    }
  }

  dynamic "threshold_rules" {
    for_each = toset([0.5, 0.8, 1.0])
    content {
      threshold_percent = threshold_rules.value
      spend_basis       = "CURRENT_SPEND"
    }
  }

  threshold_rules {
    threshold_percent = 1.0
    spend_basis       = "FORECASTED_SPEND"
  }

  all_updates_rule {
    monitoring_notification_channels = []
    disable_default_iam_recipients   = false
    enable_project_level_recipients  = true
  }

  depends_on = [google_project_service.required]
}
