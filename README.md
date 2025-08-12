# goplay

Wonder how Go 1.25's Container-aware GOMAXPROCS effects your program? goplay
dumps relevant info based on
https://github.com/golang/go/issues/73193#user-content-proposal

```
podman run --rm --cpus 2.5 -t -i debian:trixie /bin/bash
apt update && apt install golang curl
go run github.com/schmichael/goplay@latest
```
