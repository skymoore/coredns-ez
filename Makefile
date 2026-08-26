.PHONY: ui test

ui:
	npm --prefix admin/ui ci
	npm --prefix admin/ui run build

test:
	go test ./...
