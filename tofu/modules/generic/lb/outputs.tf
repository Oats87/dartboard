output "config" {
  value = {
    context = var.name

    // addresses of the Kubernetes API server
    kubernetes_addresses = {
      // resolvable over the Internet
      public = "https://${module.load_balancer.public_name}:6443"
      // resolvable from the network this cluster runs in
      private = "https://${module.load_balancer.private_name}:6443"
      // resolvable from the host running OpenTofu when create_tunnels == true
      #tunnel = local.local_kubernetes_api_url
    }

    node_access_command = {
      (module.load_balancer.name) = module.load_balancer.ssh_script_filename
    }

    ingress_class_name          = null
    lb_node = module.load_balancer
  }
}
