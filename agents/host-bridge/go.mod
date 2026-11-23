module devorbit.net/agents/host-bridge

go 1.26.0

require (
	github.com/fsnotify/fsnotify v1.8.0
	github.com/gorilla/websocket v1.5.3
	golang.org/x/crypto v0.59.0
)

require (
	cloud.google.com/go v0.116.0 // indirect
	cloud.google.com/go/auth v0.9.3 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/s2a-go v0.1.8 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.4 // indirect
	github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e // indirect
	go.opencensus.io v0.24.0 // indirect
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/text v0.44.0 // indirect
	google.golang.org/genai v1.79.1 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260414002931-afd174a4e478 // indirect
	google.golang.org/grpc v1.82.1 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

require (
	devorbit.net/agents/ai-router v0.0.0
	devorbit.net/services/connection-broker v0.0.0-00010101000000-000000000000
	golang.org/x/sys v0.50.0 // indirect
)

replace devorbit.net/services/connection-broker => ../../services/connection-broker

replace devorbit.net/agents/ai-router => ../ai-router
