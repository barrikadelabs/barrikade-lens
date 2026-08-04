variable "project_id" {
  description = "Globally unique Google Cloud project ID."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.project_id))
    error_message = "project_id must be a valid 6-30 character Google Cloud project ID."
  }
}

variable "project_name" {
  description = "Human-readable Google Cloud project name."
  type        = string
}

variable "environment" {
  description = "Deployment boundary."
  type        = string

  validation {
    condition     = contains(["test", "prod"], var.environment)
    error_message = "environment must be test or prod."
  }
}

variable "organization_id" {
  description = "Numeric Google Cloud organization ID."
  type        = string
}

variable "folder_id" {
  description = "Optional numeric folder ID. The project is placed directly under the organization when null."
  type        = string
  default     = null
  nullable    = true
}

variable "billing_account" {
  description = "Billing account ID without the billingAccounts/ prefix."
  type        = string
  sensitive   = true
}

variable "region" {
  description = "Primary regional location."
  type        = string
  default     = "europe-west2"
}

variable "budget_eur" {
  description = "Monthly project budget alert amount in EUR. This is not a hard cap."
  type        = number

  validation {
    condition     = var.budget_eur > 0
    error_message = "budget_eur must be positive."
  }
}

variable "github_owner_id" {
  description = "Immutable numeric GitHub organization ID."
  type        = string
}

variable "github_repository" {
  description = "Exact owner/repository allowed to deploy."
  type        = string
}

variable "deployment_targets" {
  description = "Partner slug to protected GitHub environment. Creates one isolated release identity/provider per target."
  type = map(object({
    github_environment = string
  }))

  validation {
    condition     = length(var.deployment_targets) > 0 && alltrue([for slug in keys(var.deployment_targets) : can(regex("^[a-z][a-z0-9-]{1,15}$", slug))])
    error_message = "At least one deployment target is required; keys must be 2-16 character DNS-safe partner slugs."
  }
}

variable "labels" {
  description = "Additional project labels."
  type        = map(string)
  default     = {}
}
