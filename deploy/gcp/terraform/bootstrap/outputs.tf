output "project_id" {
  value = google_project.environment.project_id
}

output "project_number" {
  value = google_project.environment.number
}

output "terraform_state_bucket" {
  value = google_storage_bucket.terraform_state.name
}
