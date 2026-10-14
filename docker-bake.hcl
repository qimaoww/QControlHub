variable "VERSION" { default = "dev" }
variable "CACHE_SCOPE" { default = "qch-v2" }

group "default" {
  targets = ["qcontrol-plane", "qagent", "qcontrol-web"]
}
target "common" {
  context = "."
  dockerfile = "Dockerfile"
  args = { VERSION = VERSION }
  output = ["type=cacheonly"]
}
target "qcontrol-plane" {
  inherits = ["common"]
  target = "qcontrol-plane"
  cache-from = ["type=gha,scope=${CACHE_SCOPE}-qcontrol-plane"]
  cache-to = ["type=gha,mode=max,scope=${CACHE_SCOPE}-qcontrol-plane"]
}
target "qagent" {
  inherits = ["common"]
  target = "qagent"
  cache-from = ["type=gha,scope=${CACHE_SCOPE}-qagent"]
  cache-to = ["type=gha,mode=max,scope=${CACHE_SCOPE}-qagent"]
}
target "qcontrol-web" {
  inherits = ["common"]
  target = "qcontrol-web"
  cache-from = ["type=gha,scope=${CACHE_SCOPE}-qcontrol-web"]
  cache-to = ["type=gha,mode=max,scope=${CACHE_SCOPE}-qcontrol-web"]
}
