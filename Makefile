.PHONY: ui test docker

ui:
	npm --prefix admin/ui ci
	npm --prefix admin/ui run build

test:
	go test ./...

docker:
	docker build -t ghcr.io/skymoore/coredns-ez:local .
