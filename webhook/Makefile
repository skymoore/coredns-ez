.PHONY: test image kustomize

test:
	go -C src test ./...

image:
	docker build -t ghcr.io/skymoore/coredns-ez-webhook:local .

kustomize:
	kubectl kustomize deploy
	kubectl kustomize deploy/examples
