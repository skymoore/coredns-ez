package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/cert-manager/cert-manager/pkg/acme/webhook"
	"github.com/cert-manager/cert-manager/pkg/acme/webhook/apis/acme/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

type solver struct {
	client  kubernetes.Interface
	dial    func(base, token string) *ezClient
	tokenFn func(ctx context.Context, cfg solverConfig, challengeNS string) (string, error)
	// podNS is the webhook's own namespace (cert-manager). ClusterIssuer
	// secrets live here; Challenge.ResourceNamespace is the Certificate ns.
	podNS string
}

var _ webhook.Solver = (*solver)(nil)

func (s *solver) Name() string { return solverName }

func (s *solver) Initialize(kubeClientConfig *rest.Config, _ <-chan struct{}) error {
	cl, err := kubernetes.NewForConfig(kubeClientConfig)
	if err != nil {
		return err
	}
	s.client = cl
	if s.dial == nil {
		s.dial = newClient
	}
	if s.podNS == "" {
		s.podNS = readPodNamespace()
	}
	return nil
}

func readPodNamespace() string {
	b, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (s *solver) Present(ch *v1alpha1.ChallengeRequest) error {
	ctx := context.Background()
	cli, origin, ttl, err := s.session(ctx, ch)
	if err != nil {
		return err
	}
	klog.Infof("present TXT %s in zone %s", ch.ResolvedFQDN, origin)
	if err := cli.PutTXT(ctx, origin, ch.ResolvedFQDN, ch.Key, ttl); err != nil {
		return fmt.Errorf("present %s: %w", ch.ResolvedFQDN, err)
	}
	return nil
}

func (s *solver) CleanUp(ch *v1alpha1.ChallengeRequest) error {
	ctx := context.Background()
	cli, origin, _, err := s.session(ctx, ch)
	if err != nil {
		return err
	}
	klog.Infof("cleanup TXT %s in zone %s", ch.ResolvedFQDN, origin)
	if err := cli.DeleteTXT(ctx, origin, ch.ResolvedFQDN, ch.Key); err != nil {
		return fmt.Errorf("cleanup %s: %w", ch.ResolvedFQDN, err)
	}
	return nil
}

func (s *solver) session(ctx context.Context, ch *v1alpha1.ChallengeRequest) (*ezClient, string, int, error) {
	cfg, err := loadConfig(ch.Config)
	if err != nil {
		return nil, "", 0, err
	}
	token, err := s.token(ctx, cfg, ch.ResourceNamespace)
	if err != nil {
		return nil, "", 0, err
	}
	dial := s.dial
	if dial == nil {
		dial = newClient
	}
	cli := dial(cfg.ServerURL, token)
	origin := canonicalOrigin(cfg.Zone)
	if origin == "" {
		origin = canonicalOrigin(ch.ResolvedZone)
	}
	if origin == "" {
		zones, err := cli.ListZones(ctx)
		if err != nil {
			return nil, "", 0, fmt.Errorf("list zones: %w", err)
		}
		origin, err = matchZone(ch.ResolvedFQDN, "", zones)
		if err != nil {
			return nil, "", 0, err
		}
	}
	return cli, origin, cfg.ttl(), nil
}

func (s *solver) token(ctx context.Context, cfg solverConfig, challengeNS string) (string, error) {
	if s.tokenFn != nil {
		return s.tokenFn(ctx, cfg, challengeNS)
	}
	sec := cfg.secret()
	ns := challengeNS
	if sec.Namespace != "" {
		ns = sec.Namespace
	}
	if ns == "" {
		ns = s.podNS
	}
	if ns == "" {
		return "", fmt.Errorf("token secret namespace is empty")
	}
	if s.client == nil {
		return "", fmt.Errorf("kubernetes client not initialized")
	}
	secret, err := s.client.CoreV1().Secrets(ns).Get(ctx, sec.Name, metav1.GetOptions{})
	if err != nil && sec.Namespace == "" && s.podNS != "" && s.podNS != ns &&
		(apierrors.IsNotFound(err) || apierrors.IsForbidden(err)) {
		secret, err = s.client.CoreV1().Secrets(s.podNS).Get(ctx, sec.Name, metav1.GetOptions{})
		if err == nil {
			ns = s.podNS
		}
	}
	if err != nil {
		return "", fmt.Errorf("secret %s/%s: %w", ns, sec.Name, err)
	}
	raw, ok := secret.Data[sec.Key]
	if !ok {
		return "", fmt.Errorf("secret %s/%s: key %q not found", ns, sec.Name, sec.Key)
	}
	tok := strings.TrimSpace(string(raw))
	if tok == "" {
		return "", fmt.Errorf("secret %s/%s key %q is empty", ns, sec.Name, sec.Key)
	}
	return tok, nil
}
