/*
It connects the project to the organisation and billing account.
*/

locals {
  common_labels = merge(var.labels, {
    application  = "barrikade-lens"
    environment  = var.environment
    "managed-by" = "terraform"
  })
}

resource "google_project" "environment" {
  project_id          = var.project_id
  name                = var.project_name
  billing_account     = var.billing_account
  org_id              = var.folder_id == null ? var.organization_id : null
  folder_id           = var.folder_id
  labels              = local.common_labels
  auto_create_network = false

  lifecycle {
    prevent_destroy = true
  }
}

resource "google_project_service" "storage" {
  project            = google_project.environment.project_id
  service            = "storage.googleapis.com"
  disable_on_destroy = false
}

resource "google_storage_bucket" "terraform_state" {
  project                     = google_project.environment.project_id
  name                        = "${var.project_id}-terraform-state"
  location                    = var.region
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  labels                      = local.common_labels

  versioning {
    enabled = true
  }

  retention_policy {
    retention_period = var.environment == "prod" ? 2592000 : 604800
  }

  lifecycle {
    prevent_destroy = true
  }

  depends_on = [google_project_service.storage]
}
