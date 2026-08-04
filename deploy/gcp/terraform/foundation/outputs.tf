output "project_id" {
  value = data.google_project.environment.project_id
}

output "project_number" {
  value = data.google_project.environment.number
}

output "artifact_repository" {
  value = "${var.region}-docker.pkg.dev/${data.google_project.environment.project_id}/${google_artifact_registry_repository.lens.repository_id}"
}

output "terraform_state_bucket" {
  value = "${var.project_id}-terraform-state"
}

output "release_service_account" {
  value = { for slug, account in google_service_account.release : slug => account.email }
}

output "workload_identity_provider" {
  value = { for slug, provider in google_iam_workload_identity_pool_provider.github : slug => provider.name }
}
