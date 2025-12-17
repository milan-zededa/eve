module github.com/lf-edge/eve/8021x/scep-client

go 1.23.0

require (
	github.com/go-kit/kit v0.13.0
	github.com/lf-edge/eve-api/go v0.0.0-20251216175742-df11a8a55bd5
	github.com/micromdm/scep/v2 v2.3.0
	github.com/pkg/errors v0.9.1
	github.com/smallstep/scep v0.0.0-20250318231241-a25cabb69492
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/go-kit/log v0.2.0 // indirect
	github.com/go-logfmt/logfmt v0.5.1 // indirect
	github.com/gorilla/context v0.0.0-20160226214623-1ea25387ff6f // indirect
	github.com/gorilla/mux v1.4.0 // indirect
	github.com/groob/finalizer v0.0.0-20170707115354-4c2ed49aabda // indirect
	github.com/smallstep/pkcs7 v0.2.1 // indirect
	golang.org/x/crypto v0.33.0 // indirect
)

replace github.com/lf-edge/eve-api/go => github.com/milan-zededa/eve-api/go v0.0.0-20251217162504-9cba6decde25
