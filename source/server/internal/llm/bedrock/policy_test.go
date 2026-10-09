package bedrock

import (
	"context"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"cercano/source/server/internal/modelpolicy"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
)

func TestPolicyClientPreservesAWSHTTPConfiguration(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	base := awshttp.NewBuildableClient().WithTimeout(2 * time.Second).WithTransportOptions(func(tr *http.Transport) { tr.TLSClientConfig.RootCAs = roots })
	client, err := policyHTTPClient(base)
	if err != nil {
		t.Fatal(err)
	}
	if client.Timeout != 2*time.Second {
		t.Fatal("AWS timeout lost")
	}
	ctx := modelpolicy.WithAuthority(context.Background(), modelpolicy.AuthorizeFunc(func(context.Context, modelpolicy.Attempt) error { return nil }))
	request, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/model/approved/converse", nil)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("configured CA lost:", err)
	}
	response.Body.Close()
}
