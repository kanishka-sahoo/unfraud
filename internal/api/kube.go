package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/kanishka-sahoo/unfraud/internal/profile/registry"
)

// KubeReplays creates replayer Jobs through the Kubernetes API with the
// pod's service account, mirroring deploy/helm/unfraud/templates/replay-job.yaml.
type KubeReplays struct {
	Namespace, Image, Brokers, DataPath string
	client                              *http.Client
	host, token                         string
}

const saDir = "/var/run/secrets/kubernetes.io/serviceaccount"

// InCluster configures KubeReplays from the pod's environment.
func InCluster(namespace, image, brokers, dataPath string) (*KubeReplays, error) {
	token, err := os.ReadFile(saDir + "/token")
	if err != nil {
		return nil, err
	}
	ca, err := os.ReadFile(saDir + "/ca.crt")
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca)
	return &KubeReplays{
		Namespace: namespace, Image: image, Brokers: brokers, DataPath: dataPath,
		client: &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}},
		host:   "https://" + os.Getenv("KUBERNETES_SERVICE_HOST") + ":" + os.Getenv("KUBERNETES_SERVICE_PORT"),
		token:  string(token),
	}, nil
}

func (k *KubeReplays) do(ctx context.Context, method, path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, k.host+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+k.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := k.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode >= 300 {
		return fmt.Errorf("kubernetes %s %s: %s: %s", method, path, res.Status, raw)
	}
	return json.Unmarshal(raw, out)
}

type jobList struct {
	Items []struct {
		Metadata struct {
			Name              string            `json:"name"`
			Labels            map[string]string `json:"labels"`
			Annotations       map[string]string `json:"annotations"`
			CreationTimestamp time.Time         `json:"creationTimestamp"`
		} `json:"metadata"`
		Status struct {
			Succeeded      int        `json:"succeeded"`
			Failed         int        `json:"failed"`
			CompletionTime *time.Time `json:"completionTime"`
		} `json:"status"`
	} `json:"items"`
}

func (k *KubeReplays) List(ctx context.Context) ([]Replay, error) {
	var jl jobList
	err := k.do(ctx, http.MethodGet, "/apis/batch/v1/namespaces/"+k.Namespace+"/jobs?labelSelector=app.kubernetes.io%2Fname%3Dreplayer", nil, &jl)
	if err != nil {
		return nil, err
	}
	out := []Replay{}
	for _, j := range jl.Items {
		r := Replay{Name: j.Metadata.Name, Profile: j.Metadata.Labels["unfraud.dev/profile"], Status: "running",
			StartedAt: j.Metadata.CreationTimestamp, EndedAt: j.Status.CompletionTime}
		r.Speed, _ = strconv.ParseFloat(j.Metadata.Annotations["unfraud.dev/speed"], 64)
		switch {
		case j.Status.Succeeded > 0:
			r.Status = "succeeded"
		case j.Status.Failed > 0:
			r.Status = "failed"
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b Replay) int { return b.StartedAt.Compare(a.StartedAt) })
	return out, nil
}

func (k *KubeReplays) Start(ctx context.Context, req ReplayRequest) (Replay, error) {
	if _, err := registry.Get(req.Profile); err != nil {
		return Replay{}, err
	}
	name := fmt.Sprintf("replay-%s-%s", req.Profile, time.Now().UTC().Format("20060102150405"))
	speed := strconv.FormatFloat(req.Speed, 'f', -1, 64)
	f := false
	job := map[string]any{
		"apiVersion": "batch/v1", "kind": "Job",
		"metadata": map[string]any{
			"name": name,
			"labels": map[string]string{"app.kubernetes.io/name": "replayer", "unfraud.dev/profile": req.Profile,
				"app.kubernetes.io/part-of": "unfraud"},
			"annotations": map[string]string{"unfraud.dev/speed": speed},
		},
		"spec": map[string]any{
			"backoffLimit": 0, "ttlSecondsAfterFinished": 86400,
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]string{"app.kubernetes.io/name": "replayer"}},
				"spec": map[string]any{
					"restartPolicy": "Never",
					"containers": []any{map[string]any{
						"name": "replayer", "image": k.Image, "imagePullPolicy": "IfNotPresent",
						"args": []string{"-profile=" + req.Profile, "-data=/data", "-brokers=" + k.Brokers,
							"-speed=" + speed, "-limit=" + strconv.Itoa(req.Limit)},
						"env":       []any{map[string]string{"name": "GOMEMLIMIT", "value": "400MiB"}},
						"resources": map[string]any{"requests": map[string]string{"cpu": "500m", "memory": "512Mi"}, "limits": map[string]string{"memory": "512Mi"}},
						"securityContext": map[string]any{"allowPrivilegeEscalation": &f, "readOnlyRootFilesystem": true,
							"capabilities": map[string]any{"drop": []string{"ALL"}}},
						"volumeMounts": []any{map[string]any{"name": "data", "mountPath": "/data", "readOnly": true}},
					}},
					"volumes": []any{map[string]any{"name": "data", "hostPath": map[string]string{"path": k.DataPath, "type": "Directory"}}},
				},
			},
		},
	}
	var created map[string]any
	if err := k.do(ctx, http.MethodPost, "/apis/batch/v1/namespaces/"+k.Namespace+"/jobs", job, &created); err != nil {
		return Replay{}, err
	}
	return Replay{Name: name, Profile: req.Profile, Speed: req.Speed, Status: "running", StartedAt: time.Now().UTC()}, nil
}
