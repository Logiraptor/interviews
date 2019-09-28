
variable "creds" {}

variable "project" {}

provider "google" {
  credentials = var.creds
  project     = var.project
  region      = "us-central1"
}
