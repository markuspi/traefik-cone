<div align="center">

  <img src="./.assets/icon.png" width="150" alt="Traefik Cone Plugin Icon">

</div>

# Traefik Cone Plugin

This plugin manages dynamic IP allowlists.
IPs get allowlisted by browsing a configured (hidden) endpoint.# Traefik Cone Plugin

<div align="center">

  <img src="./.assets/icon.png" width="128" alt="Traefik Cone Plugin Icon">

</div>

## Installation

``` yaml
# static.yaml
experimental:
  plugins:
    traefik-cone:
      moduleName: "github.com/markuspi/traefik-cone"
      version: "v1.0.0"

providers:
  plugin:
    traefik-cone:
      # config options
      expiration: "24h"
```

## Usage

- Create a router to expose the unlocking service `service@traefik-cone`.
You may restrict the route to a certain subpath or add authentication middlewares.
- Add the HTTP or TCP middleware `middleware@traefik-cone` to the routes that you want to protect.
- All IPs that browse the unlocking service are added to the allowlist and are granted access through the middleware.

``` yaml
# dynamic.yaml
http:
  routers:
    unlock-endpoint:
      rule: "Host(`cone.example.com`) && Path(`/M9HcGYBm4C6KSTgCoZC1`)"
      service: "service@traefik-cone"
    
    protected-http-endpoint:
      # ...
      middlewares:
        - "middleware@traefik-cone"

tcp:
  routers:
    protected-tcp-endpoint:
      # ...
      middlewares:
        - "middleware@traefik-cone" 
``