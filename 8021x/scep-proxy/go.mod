module github.com/lf-edge/eve/8021x/scep-proxy

go 1.23.0

replace github.com/lf-edge/eve-api/go => github.com/milan-zededa/eve-api/go v0.0.0-20251217162504-9cba6decde25

require (
	github.com/lf-edge/eve-api/go v0.0.0-00010101000000-000000000000
	github.com/pkg/errors v0.9.1
	google.golang.org/protobuf v1.36.11
)
