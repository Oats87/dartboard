terraform {
  required_providers {
    ssh = {
      source = "loafoe/ssh"
    }
  }
}


module "load_balancer" {
  source               = "../node"
  project_name         = var.project_name
  name                 = "${var.name}"
  ssh_private_key_path = var.ssh_private_key_path
  ssh_user             = var.ssh_user
  ssh_tunnels = []
  node_module           = var.node_module
  node_module_variables = var.node_module_variables
  network_config        = var.network_config
}

resource "ssh_sensitive_resource" "load_balancer_setup" {
  host         = module.load_balancer.private_name
  private_key  = file(var.ssh_private_key_path)
  user         = var.ssh_user
  bastion_host = var.network_config.ssh_bastion_host
  bastion_user = var.network_config.ssh_bastion_user
  timeout      = "600s"

  file {
    content = templatefile("${path.module}/nginx.conf.tmpl", {
      servers = var.servers
    })
    destination = "/tmp/nginx.conf"
    permissions = "0644"
  }

  file {
    content     = file("${path.module}/container-nginx.service.tmpl")
    destination = "/tmp/container-nginx.service"
    permissions = "0700"
  }

  commands = [
    "sudo zypper install -y podman",
    "sudo mkdir -p /etc/nginx",
    "sudo cp /tmp/nginx.conf /etc/nginx/nginx.conf",
    "sudo cp /tmp/container-nginx.service /etc/systemd/system",
    "sudo systemctl daemon-reload",
    "sudo systemctl enable --now container-nginx",
  ]
}
