package openshift

import (
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	utilnet "k8s.io/apimachinery/pkg/util/net"
)

func isRetryableAPIError(err error) bool {
	// These errors may indicate a transient error that we can retry in tests.
	if apierrors.IsInternalError(err) || apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) ||
		apierrors.IsTooManyRequests(err) || utilnet.IsProbableEOF(err) || utilnet.IsConnectionReset(err) ||
		utilnet.IsConnectionRefused(err) || utilnet.IsHTTP2ConnectionLost(err) {
		return true
	}

	// If the error sends the Retry-After header, we respect it as an explicit confirmation we should retry.
	if _, shouldRetry := apierrors.SuggestsClientDelay(err); shouldRetry {
		return true
	}

	// "etcdserver: request timed out" does not seem to match the timeout errors above
	if strings.Contains(err.Error(), "etcdserver: request timed out") {
		return true
	}

	// "unable to upgrade connection" happens occasionally when executing commands in Pods
	if strings.Contains(err.Error(), "unable to upgrade connection") {
		return true
	}

	// "transport is closing" is an internal gRPC err, we can not use ErrConnClosing
	if strings.Contains(err.Error(), "transport is closing") {
		return true
	}

	// "transport: missing content-type field" is an error that sometimes
	// is returned while talking to the kubernetes-api-server. There does
	// not seem to be a public error constant for this.
	if strings.Contains(err.Error(), "transport: missing content-type field") {
		return true
	}

	return false
}
