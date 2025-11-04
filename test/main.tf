terraform {
  required_version = ">= 1.6.0"
}

# Simple local file resource to demonstrate tracing
resource "local_file" "example" {
  content  = "Hello from OpenTofu with tracing!"
  filename = "${path.module}/output.txt"
}

# Random resource to make the trace more interesting
resource "random_id" "example" {
  byte_length = 8
}

resource "local_file" "random" {
  content  = "Random ID: ${random_id.example.hex}"
  filename = "${path.module}/random.txt"
}

output "random_id" {
  value = random_id.example.hex
}
