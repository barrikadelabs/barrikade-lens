variable "project_id" {
  description = "Existing test or production project created by the foundation."
  type        = string
}

variable "environment" {
  type = string
  validation {
    condition     = contains(["test", "prod"], var.environment)
    error_message = "environment must be test or prod."
  }
}

variable "suspended" {
  description = "Pause billable runtime capacity and block public traffic while preserving data and configuration."
  type        = bool
  default     = false
}

variable "database_suspended" {
  description = "Stop the preserved Cloud SQL instance after the application has been suspended."
  type        = bool
  default     = false
}

variable "region" {
  type    = string
  default = "europe-west2"
}

variable "partner_slug" {
  description = "Short DNS-safe partner identifier."
  type        = string
  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{1,15}$", var.partner_slug))
    error_message = "partner_slug must be 2-16 lowercase letters, digits, or hyphens and start with a letter."
  }
}

variable "organization_id" {
  description = "Lens organization ID used inside the application."
  type        = string
}

variable "organization_name" {
  description = "Partner-facing Lens organization name."
  type        = string
}

variable "public_url" {
  description = "Stable external HTTPS URL, without a trailing slash."
  type        = string
  validation {
    condition     = can(regex("^https://[^/]+$", var.public_url))
    error_message = "public_url must be an HTTPS origin without a trailing slash or path."
  }
}

variable "image" {
  description = "Lens container image pinned to an immutable sha256 digest."
  type        = string
  validation {
    condition     = can(regex("@sha256:[0-9a-f]{64}$", var.image))
    error_message = "image must end in an immutable @sha256:<64 lowercase hex characters> digest."
  }
}

variable "release_service_account" {
  description = "Partner-specific release service account created by the foundation."
  type        = string
  validation {
    condition     = can(regex("^lens-[a-z][a-z0-9-]+-release@.+\\.iam\\.gserviceaccount\\.com$", var.release_service_account))
    error_message = "release_service_account must be the partner-specific lens-<slug>-release service account."
  }
}

variable "oidc_issuer" { type = string }
variable "oidc_client_id" { type = string }
variable "oidc_redirect_uri" { type = string }
variable "oidc_admin_group" { type = string }
variable "oidc_client_secret" {
  description = "Optional OIDC client secret. Null is valid for a public PKCE client. Stored in sensitive remote Terraform state when supplied."
  type        = string
  default     = null
  nullable    = true
  sensitive   = true
}

variable "github_app_id" {
  type     = string
  default  = null
  nullable = true
}
variable "github_private_key" {
  type      = string
  default   = null
  nullable  = true
  sensitive = true
}
variable "github_webhook_secret" {
  type      = string
  default   = null
  nullable  = true
  sensitive = true
}

variable "notification_channel_ids" {
  description = "Existing Cloud Monitoring notification channel resource names."
  type        = list(string)
  default     = []
}

variable "labels" {
  type    = map(string)
  default = {}
}

variable "sql_tier" {
  description = "Optional Cloud SQL tier override."
  type        = string
  default     = null
  nullable    = true
}
