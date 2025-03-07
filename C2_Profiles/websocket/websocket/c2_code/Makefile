.PHONY: default
default: build ;

BINARY_NAME?=mythic_websocket_server
DEBUG_LEVEL?="debug"
MYTHIC_SERVER_HOST?="127.0.0.1"
MYTHIC_SERVER_PORT?="17443"
MYTHIC_SERVER_GRPC_PORT?="17444"

build:
	go mod download 
	go mod tidy
	CGO_ENABLED=0 go build -o ${BINARY_NAME} -tags=websocket .
	cp ${BINARY_NAME} /

local:
	go mod download 
	go mod tidy
	CGO_ENABLED=0 go build -o ${BINARY_NAME} -tags=websocket .

run:
	cp /${BINARY_NAME} .

run_custom:
	go mod download
	go mod tidy
	CGO_ENABLED=0 go build -o ${BINARY_NAME} -tags=websocket .
	DEBUG_LEVEL=${DEBUG_LEVEL} \
MYTHIC_SERVER_HOST=${MYTHIC_SERVER_HOST} \
MYTHIC_SERVER_PORT=${MYTHIC_SERVER_PORT} \
MYTHIC_SERVER_GRPC_PORT=${MYTHIC_SERVER_GRPC_PORT} \
./${BINARY_NAME}
