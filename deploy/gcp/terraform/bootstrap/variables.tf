variable "project_id" { type = string }
variable "project_name" { type = string }
variable "environment" { type = string }
variable "organization_id" { type = string }
variable "folder_id" {
  type     = string
  default  = null
  nullable = true
}
variable "billing_account" {
  type      = string
  sensitive = true
}
variable "region" { type = string }
variable "labels" {
  type    = map(string)
  default = {}
}

# Accepted for compatibility with the shared foundation variable file.
variable "budget_eur" { type = number }
variable "github_owner_id" { type = string }
variable "github_repository" { type = string }
variable "deployment_targets" {
  type = map(object({
    github_environment = string
  }))
}
