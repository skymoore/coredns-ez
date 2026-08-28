package main

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestTokenFallsBackToPodNamespace(t *testing.T) {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "coredns-api-token", Namespace: "cert-manager"},
		Data:       map[string][]byte{"token": []byte("abc")},
	}
	s := &solver{
		client: fake.NewSimpleClientset(sec),
		podNS:  "cert-manager",
	}
	cfg := solverConfig{
		ServerURL:      "https://ns1.dns.rwx.dev",
		TokenSecretRef: secretRef{Name: "coredns-api-token", Key: "token"},
	}
	tok, err := s.token(context.Background(), cfg, "coredns-ingress")
	if err != nil {
		t.Fatal(err)
	}
	if tok != "abc" {
		t.Fatalf("token %q", tok)
	}
}

func TestTokenUsesExplicitNamespace(t *testing.T) {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "coredns-api-token", Namespace: "other"},
		Data:       map[string][]byte{"token": []byte("xyz")},
	}
	s := &solver{
		client: fake.NewSimpleClientset(sec),
		podNS:  "cert-manager",
	}
	cfg := solverConfig{
		ServerURL:      "https://ns1.dns.rwx.dev",
		TokenSecretRef: secretRef{Name: "coredns-api-token", Key: "token", Namespace: "other"},
	}
	tok, err := s.token(context.Background(), cfg, "coredns-ingress")
	if err != nil || tok != "xyz" {
		t.Fatalf("tok=%q err=%v", tok, err)
	}
}
