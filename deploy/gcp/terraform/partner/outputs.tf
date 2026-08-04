output "cloud_run_uri" {
  value = google_cloud_run_v2_service.lens.uri
}

output "cloud_run_service" {
  value = google_cloud_run_v2_service.lens.name
}

output "runtime_service_account" {
  value = google_service_account.runtime.email
}

output "cloud_sql_connection_name" {
  value = google_sql_database_instance.lens.connection_name
}

output "health_check_id" {
  value = google_monitoring_uptime_check_config.health.uptime_check_id
}

output "test_admin_secret" {
  description = "Secret Manager name containing the temporary test-only sign-in token; null in production."
  value       = var.environment == "test" ? google_secret_manager_secret.dev_admin[0].secret_id : null
}

output "suspended" {
  description = "Whether runtime capacity and public access are paused."
  value       = var.suspended
}
