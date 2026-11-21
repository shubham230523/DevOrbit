module devorbit.net/agents/host-bridge

go 1.26.0

require (
	github.com/fsnotify/fsnotify v1.8.0
	github.com/gorilla/websocket v1.5.3
	golang.org/x/crypto v0.59.0
)

require (
	devorbit.net/services/connection-broker v0.0.0-00010101000000-000000000000 // indirect
	golang.org/x/sys v0.50.0 // indirect
)

replace devorbit.net/services/connection-broker => ../../services/connection-broker
