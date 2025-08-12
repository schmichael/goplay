job "podman-goplay" {
  type = "batch"

  group "goplay" {
    task "goplay" {
      driver = "podman"

      config {
        image   = "debian:trixie"
        command = "/bin/bash"
        args    = ["-c", "apt update && apt install --yes curl golang && go run github.com/schmichael/goplay@latest"]
      }

      resources {
        cpu    = 500
        #cores  = 3
        memory = 256
      }

      restart {
        attempts = 0
      }
    }
  }
}
