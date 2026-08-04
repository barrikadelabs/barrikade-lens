#Creates the actual Lens environment

data "google_project" "current" {
  project_id = var.project_id
}

locals {
  service_name   = "lens-${var.partner_slug}"
  github_enabled = var.github_app_id != null && var.github_private_key != null && var.github_webhook_secret != null
  sql_tier       = coalesce(var.sql_tier, var.environment == "prod" ? "db-custom-2-7680" : "db-f1-micro")
  min_instances  = var.environment == "prod" ? 2 : 1
  max_instances  = var.environment == "prod" ? 10 : 3
  concurrency    = var.environment == "prod" ? 40 : 20
  common_labels = merge(var.labels, {
    application  = "barrikade-lens"
    environment  = var.environment
    partner      = var.partner_slug
    "managed-by" = "terraform"
  })
}

resource "terraform_data" "validate_github" {
  lifecycle {
    precondition {
      condition = (
        (var.github_app_id == null && var.github_private_key == null && var.github_webhook_secret == null) ||
        local.github_enabled
      )
      error_message = "Set github_app_id, github_private_key, and github_webhook_secret together, or leave all three null."
    }
  }
}

resource "terraform_data" "validate_suspension" {
  lifecycle {
    precondition {
      condition     = !var.database_suspended || var.suspended
      error_message = "database_suspended can be true only when suspended is also true."
    }
  }
}

resource "google_service_account" "runtime" {
  project      = var.project_id
  account_id   = "${local.service_name}-runtime"
  display_name = "Lens ${var.partner_slug} runtime"
  description  = "Runtime identity isolated to ${var.partner_slug} in ${var.environment}"
}

resource "google_project_iam_member" "cloudsql_client" {
  project = var.project_id
  role    = "roles/cloudsql.client"
  member  = "serviceAccount:${google_service_account.runtime.email}"
}

resource "google_service_account_iam_member" "release_can_deploy_runtime" {
  service_account_id = google_service_account.runtime.name
  role               = "roles/iam.serviceAccountUser"
  member             = "serviceAccount:${var.release_service_account}"
}

resource "google_compute_network" "partner" {
  project                 = var.project_id
  name                    = "${local.service_name}-network"
  auto_create_subnetworks = false
  routing_mode            = "REGIONAL"
}

resource "google_compute_subnetwork" "cloud_run" {
  project       = var.project_id
  name          = "${local.service_name}-cloud-run"
  region        = var.region
  network       = google_compute_network.partner.id
  ip_cidr_range = "10.20.0.0/26"
}

resource "google_compute_global_address" "private_services" {
  project       = var.project_id
  name          = "${local.service_name}-private-services"
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  address       = "10.30.0.0"
  prefix_length = 16
  network       = google_compute_network.partner.id
}

resource "google_service_networking_connection" "private_services" {
  network                 = google_compute_network.partner.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.private_services.name]
}

resource "google_project_iam_member" "cloud_run_network_user" {
  project = var.project_id
  role    = "roles/compute.networkUser"
  member  = "serviceAccount:service-${data.google_project.current.number}@serverless-robot-prod.iam.gserviceaccount.com"
}

resource "random_password" "database" {
  length  = 40
  special = false
}

resource "random_password" "jwt" {
  length  = 64
  special = false
}

resource "random_password" "dev_admin" {
  count   = var.environment == "test" ? 1 : 0
  length  = 48
  special = false
}

resource "google_sql_database_instance" "lens" {
  project             = var.project_id
  name                = "${local.service_name}-${var.environment}"
  region              = var.region
  database_version    = "POSTGRES_18"
  deletion_protection = var.environment == "prod"

  settings {
    activation_policy = var.database_suspended ? "NEVER" : "ALWAYS"
    tier              = local.sql_tier
    edition           = "ENTERPRISE"
    availability_type = var.environment == "prod" ? "REGIONAL" : "ZONAL"
    disk_type         = "PD_SSD"
    disk_size         = var.environment == "prod" ? 50 : 10
    disk_autoresize   = true
    user_labels       = local.common_labels

    ip_configuration {
      ipv4_enabled    = false
      private_network = google_compute_network.partner.id
    }

    backup_configuration {
      enabled                        = true
      point_in_time_recovery_enabled = true
      start_time                     = "02:00"
      transaction_log_retention_days = var.environment == "prod" ? 7 : 3

      backup_retention_settings {
        retained_backups = var.environment == "prod" ? 14 : 7
        retention_unit   = "COUNT"
      }
    }

    maintenance_window {
      day          = 7
      hour         = 3
      update_track = "stable"
    }
  }

  lifecycle {
    prevent_destroy = true
  }

  depends_on = [google_service_networking_connection.private_services]
}

resource "google_sql_database" "lens" {
  project  = var.project_id
  name     = "lens"
  instance = google_sql_database_instance.lens.name
}

resource "google_sql_user" "lens" {
  project  = var.project_id
  name     = "lens"
  instance = google_sql_database_instance.lens.name
  password = random_password.database.result
}

locals {
  database_url = "postgresql://lens:${urlencode(random_password.database.result)}@/lens?host=${urlencode("/cloudsql/${google_sql_database_instance.lens.connection_name}")}&sslmode=disable"
}

resource "google_secret_manager_secret" "database_url" {
  project   = var.project_id
  secret_id = "${local.service_name}-database-url"
  labels    = local.common_labels
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "database_url" {
  secret      = google_secret_manager_secret.database_url.id
  secret_data = local.database_url
}

resource "google_secret_manager_secret" "jwt" {
  project   = var.project_id
  secret_id = "${local.service_name}-jwt"
  labels    = local.common_labels
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "jwt" {
  secret      = google_secret_manager_secret.jwt.id
  secret_data = random_password.jwt.result
}

resource "google_secret_manager_secret" "dev_admin" {
  count     = var.environment == "test" ? 1 : 0
  project   = var.project_id
  secret_id = "${local.service_name}-dev-admin"
  labels    = local.common_labels
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "dev_admin" {
  count       = var.environment == "test" ? 1 : 0
  secret      = google_secret_manager_secret.dev_admin[0].id
  secret_data = random_password.dev_admin[0].result
}

resource "google_secret_manager_secret" "oidc_client" {
  count     = var.oidc_client_secret == null ? 0 : 1
  project   = var.project_id
  secret_id = "${local.service_name}-oidc-client"
  labels    = local.common_labels
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "oidc_client" {
  count       = var.oidc_client_secret == null ? 0 : 1
  secret      = google_secret_manager_secret.oidc_client[0].id
  secret_data = var.oidc_client_secret
}

resource "google_secret_manager_secret" "github_key" {
  count     = local.github_enabled ? 1 : 0
  project   = var.project_id
  secret_id = "${local.service_name}-github-key"
  labels    = local.common_labels
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "github_key" {
  count       = local.github_enabled ? 1 : 0
  secret      = google_secret_manager_secret.github_key[0].id
  secret_data = var.github_private_key
}

resource "google_secret_manager_secret" "github_webhook" {
  count     = local.github_enabled ? 1 : 0
  project   = var.project_id
  secret_id = "${local.service_name}-github-webhook"
  labels    = local.common_labels
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "github_webhook" {
  count       = local.github_enabled ? 1 : 0
  secret      = google_secret_manager_secret.github_webhook[0].id
  secret_data = var.github_webhook_secret
}

resource "google_secret_manager_secret_iam_member" "runtime_database" {
  project   = var.project_id
  secret_id = google_secret_manager_secret.database_url.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.runtime.email}"
}

resource "google_secret_manager_secret_iam_member" "runtime_jwt" {
  project   = var.project_id
  secret_id = google_secret_manager_secret.jwt.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.runtime.email}"
}

resource "google_secret_manager_secret_iam_member" "runtime_dev_admin" {
  count     = var.environment == "test" ? 1 : 0
  project   = var.project_id
  secret_id = google_secret_manager_secret.dev_admin[0].secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.runtime.email}"
}

resource "google_secret_manager_secret_iam_member" "runtime_oidc" {
  count     = var.oidc_client_secret == null ? 0 : 1
  project   = var.project_id
  secret_id = google_secret_manager_secret.oidc_client[0].secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.runtime.email}"
}

resource "google_secret_manager_secret_iam_member" "runtime_github_key" {
  count     = local.github_enabled ? 1 : 0
  project   = var.project_id
  secret_id = google_secret_manager_secret.github_key[0].secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.runtime.email}"
}

resource "google_secret_manager_secret_iam_member" "runtime_github_webhook" {
  count     = local.github_enabled ? 1 : 0
  project   = var.project_id
  secret_id = google_secret_manager_secret.github_webhook[0].secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.runtime.email}"
}

resource "google_cloud_run_v2_service" "lens" {
  project              = var.project_id
  name                 = local.service_name
  location             = var.region
  ingress              = "INGRESS_TRAFFIC_ALL"
  default_uri_disabled = false
  invoker_iam_disabled = !var.suspended
  deletion_protection  = var.environment == "prod"
  labels               = local.common_labels

  lifecycle {
    ignore_changes = [client, client_version, template[0].revision]
  }

  template {
    service_account                  = google_service_account.runtime.email
    timeout                          = "40s"
    max_instance_request_concurrency = local.concurrency

    scaling {
      min_instance_count = var.suspended ? 0 : local.min_instances
      max_instance_count = local.max_instances
    }

    vpc_access {
      egress = "PRIVATE_RANGES_ONLY"
      network_interfaces {
        network    = google_compute_network.partner.name
        subnetwork = google_compute_subnetwork.cloud_run.name
      }
    }

    containers {
      image = var.image

      ports {
        container_port = 8080
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "1Gi"
        }
        cpu_idle          = false
        startup_cpu_boost = true
      }

      env {
        name  = "LENS_PUBLIC_URL"
        value = var.public_url
      }

      env {
        name  = "LENS_ORGANIZATION_ID"
        value = var.organization_id
      }

      env {
        name  = "LENS_ORGANIZATION_NAME"
        value = var.organization_name
      }

      env {
        name  = "LENS_OIDC_ISSUER"
        value = var.oidc_issuer
      }

      env {
        name  = "LENS_OIDC_CLIENT_ID"
        value = var.oidc_client_id
      }

      env {
        name  = "LENS_OIDC_REDIRECT_URI"
        value = var.oidc_redirect_uri
      }

      env {
        name  = "LENS_OIDC_ADMIN_GROUP"
        value = var.oidc_admin_group
      }

      env {
        name = "LENS_DATABASE_URL"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.database_url.secret_id
            version = google_secret_manager_secret_version.database_url.version
          }
        }
      }

      env {
        name = "LENS_JWT_SECRET"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.jwt.secret_id
            version = google_secret_manager_secret_version.jwt.version
          }
        }
      }

      dynamic "env" {
        for_each = var.environment == "test" ? [1] : []
        content {
          name = "LENS_DEV_ADMIN_TOKEN"
          value_source {
            secret_key_ref {
              secret  = google_secret_manager_secret.dev_admin[0].secret_id
              version = google_secret_manager_secret_version.dev_admin[0].version
            }
          }
        }
      }

      dynamic "env" {
        for_each = var.oidc_client_secret == null ? [] : [1]
        content {
          name = "LENS_OIDC_CLIENT_SECRET"
          value_source {
            secret_key_ref {
              secret  = google_secret_manager_secret.oidc_client[0].secret_id
              version = google_secret_manager_secret_version.oidc_client[0].version
            }
          }
        }
      }

      dynamic "env" {
        for_each = local.github_enabled ? [1] : []
        content {
          name  = "LENS_GITHUB_APP_ID"
          value = var.github_app_id
        }
      }

      dynamic "env" {
        for_each = local.github_enabled ? [1] : []
        content {
          name  = "LENS_GITHUB_PRIVATE_KEY_FILE"
          value = "/secrets/github/private-key.pem"
        }
      }

      dynamic "env" {
        for_each = local.github_enabled ? [1] : []
        content {
          name = "LENS_GITHUB_WEBHOOK_SECRET"
          value_source {
            secret_key_ref {
              secret  = google_secret_manager_secret.github_webhook[0].secret_id
              version = google_secret_manager_secret_version.github_webhook[0].version
            }
          }
        }
      }

      volume_mounts {
        name       = "cloudsql"
        mount_path = "/cloudsql"
      }

      dynamic "volume_mounts" {
        for_each = local.github_enabled ? [1] : []
        content {
          name       = "github-key"
          mount_path = "/secrets/github"
        }
      }
    }

    volumes {
      name = "cloudsql"
      cloud_sql_instance {
        instances = [google_sql_database_instance.lens.connection_name]
      }
    }

    dynamic "volumes" {
      for_each = local.github_enabled ? [1] : []
      content {
        name = "github-key"
        secret {
          secret = google_secret_manager_secret.github_key[0].secret_id
          items {
            version = google_secret_manager_secret_version.github_key[0].version
            path    = "private-key.pem"
            mode    = 256
          }
        }
      }
    }
  }

  traffic {
    type    = "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST"
    percent = 100
  }

  depends_on = [
    google_project_iam_member.cloudsql_client,
    google_project_iam_member.cloud_run_network_user,
    google_secret_manager_secret_iam_member.runtime_database,
    google_secret_manager_secret_iam_member.runtime_jwt,
    google_secret_manager_secret_iam_member.runtime_dev_admin,
    google_secret_manager_secret_iam_member.runtime_oidc,
    google_secret_manager_secret_iam_member.runtime_github_key,
    google_secret_manager_secret_iam_member.runtime_github_webhook,
    terraform_data.validate_github,
  ]
}

resource "google_cloud_run_v2_service_iam_member" "release_developer" {
  project  = var.project_id
  location = var.region
  name     = google_cloud_run_v2_service.lens.name
  role     = "roles/run.developer"
  member   = "serviceAccount:${var.release_service_account}"
}

resource "google_monitoring_uptime_check_config" "health" {
  project      = var.project_id
  display_name = "${local.service_name} public health"
  timeout      = "10s"
  period       = "60s"

  selected_regions = ["EUROPE", "USA", "ASIA_PACIFIC"]

  http_check {
    path           = "/health"
    port           = 443
    request_method = "GET"
    use_ssl        = true
    validate_ssl   = true
  }

  monitored_resource {
    type = "uptime_url"
    labels = {
      project_id = var.project_id
      host       = trimprefix(google_cloud_run_v2_service.lens.uri, "https://")
    }
  }
}

resource "google_monitoring_alert_policy" "uptime" {
  project      = var.project_id
  display_name = "${local.service_name} health check failing"
  combiner     = "OR"
  enabled      = !var.suspended

  notification_channels = var.notification_channel_ids

  conditions {
    display_name = "Health check failed for five minutes"
    condition_threshold {
      filter          = "metric.type=\"monitoring.googleapis.com/uptime_check/check_passed\" AND resource.type=\"uptime_url\" AND metric.label.check_id=\"${google_monitoring_uptime_check_config.health.uptime_check_id}\""
      comparison      = "COMPARISON_LT"
      threshold_value = 1
      duration        = "300s"

      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_NEXT_OLDER"
      }

      trigger {
        count = 1
      }
    }
  }

  alert_strategy {
    auto_close = "1800s"
  }
}
