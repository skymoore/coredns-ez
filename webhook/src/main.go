package main

import (
	"os"

	"github.com/cert-manager/cert-manager/pkg/acme/webhook/cmd"
	"k8s.io/klog/v2"
)

var groupName = os.Getenv("GROUP_NAME")

func main() {
	if groupName == "" {
		klog.Fatal("GROUP_NAME must be set (APIService group, e.g. acme.rwx.dev)")
	}
	cmd.RunWebhookServer(groupName, &solver{})
}
