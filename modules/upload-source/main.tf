variable "artifact_name" {}
variable "source_dir" {}
variable "output_dir" {}
variable "bucket" {}

data "archive_file" "source" {
  type        = "zip"
  source_dir = var.source_dir
  output_path = "${var.output_dir}/${var.artifact_name}.zip"
}

resource "google_storage_bucket_object" "source" {
  name = "${var.artifact_name}-${data.archive_file.source.output_md5}.zip"
  bucket = var.bucket
  source = data.archive_file.source.output_path
}

output "bucket_path" {
  value = google_storage_bucket_object.source.name
}
