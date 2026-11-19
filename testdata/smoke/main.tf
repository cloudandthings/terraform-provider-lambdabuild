# Smoke-test configuration: runs inside a clean container that has only
# Terraform and the cross-compiled provider (dev override). Driven by
# `mise run test:smoke`; paths below are the container mount points.
terraform {
  required_providers {
    lambdabuild = { source = "cloudandthings/lambdabuild" }
  }
}

variable "python_version" { type = string }
variable "src" { default = "/src" }
variable "out" { default = "/out" }

provider "lambdabuild" {
  cache_dir = "/cache"
}

locals {
  archs = ["x86_64", "arm64"]
  # case id -> arguments; `check.py` in the Lambda image decides what to
  # verify from the id.
  kinds = [
    { id = "numpy-function", project = "fixtures/numpy-deps", type = "function", deps = true, member = null },
    { id = "numpy-layer", project = "fixtures/numpy-deps", type = "layer", deps = true, member = null },
    { id = "layer-user", project = "smoke/layer-user", type = "function", deps = false, member = null },
    { id = "code-only", project = "fixtures/code-only", type = "function", deps = false, member = null },
    { id = "workspace", project = "fixtures/workspace/ws", type = "function", deps = true, member = "wsapp" },
    { id = "packaged", project = "fixtures/packaged", type = "function", deps = true, member = null },
  ]
  cases = {
    for pair in setproduct(local.archs, local.kinds) :
    "${pair[1].id}-${pair[0]}" => merge(pair[1], { arch = pair[0] })
  }
}

resource "lambdabuild_python_package" "case" {
  for_each = local.cases

  project_dir          = "${var.src}/${each.value.project}"
  output_dir           = "${var.out}/${each.key}"
  name                 = each.key
  python_version       = var.python_version
  architecture         = each.value.arch
  artifact_type        = each.value.type
  install_dependencies = each.value.deps
  workspace_member     = each.value.member
}

output "artifacts" {
  value = {
    for k, v in local.cases : k => {
      id   = v.id
      arch = v.arch
      type = v.type
      path = lambdabuild_python_package.case[k].output_path
    }
  }
}
