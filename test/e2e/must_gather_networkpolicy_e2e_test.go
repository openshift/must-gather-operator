//go:build e2e
// +build e2e

package e2e

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega" //nolint:revive // Match the existing Gomega assertion DSL.
	imagev1 "github.com/openshift/api/image/v1"
	mgv1 "github.com/openshift/must-gather-operator/api/v1"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	DefaultTimeout          = 3 * time.Minute
	CollectionTimeout       = 20 * time.Minute
	CaseTimeout             = 45 * time.Minute
	CleanupTimeout          = 5 * time.Minute
	ObservationWindow       = 10 * time.Second
	DefaultInterval         = time.Second
	ProbeTimeout            = 3 * time.Second
	HoldTimeout             = 15 * time.Minute
	BaselineName            = "must-gather-default"
	NetworkLabel            = "must-gather.openshift.io/network-policy"
	IdentityLabel           = "must-gather.openshift.io/job"
	ManagerLabel            = "must-gather.openshift.io/managed-by"
	ManagerValue            = "must-gather-operator"
	ReleaseFile             = "/tmp/mg368-release"
	IngressPort       int32 = 18080
)

// Fixtures names externally provisioned, disposable test services. Secrets are
// read from environment variables at execution time and never stored in reports.
type Fixtures struct {
	OperatorNamespace string         `json:"operatorNamespace"`
	Deployment        string         `json:"deployment"`
	Subscription      string         `json:"subscription"`
	GatherRole        string         `json:"gatherRole"`
	ProbeImage        string         `json:"probeImage"`
	CustomImage       string         `json:"customImage"`
	StorageClass      *string        `json:"storageClass"`
	IsolatedOperator  bool           `json:"isolatedOperator"`
	SFTP              []SFTPEndpoint `json:"sftp"`
	Proxies           ProxyFixtures  `json:"proxies"`
}

type SFTPEndpoint struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	UsernameEnv string `json:"usernameEnv"`
	PasswordEnv string `json:"passwordEnv"`
	HostKey     string `json:"hostKey"`
	Directory   string `json:"directory"`
}

type ProxyFixtures struct {
	HTTPHost       string `json:"httpHost"`
	HTTPSHost      string `json:"httpsHost"`
	HTTPPort       int    `json:"httpPort"`
	HTTPSPort      int    `json:"httpsPort"`
	SecondHTTPPort int    `json:"secondHTTPPort"`
	LogNamespace   string `json:"logNamespace"`
	LogPod         string `json:"logPod"`
	LogContainer   string `json:"logContainer"`
}

func LoadFixtures(path string) (Fixtures, error) {
	f := Fixtures{OperatorNamespace: "must-gather-operator", Deployment: "must-gather-operator", GatherRole: "must-gather-admin"}
	b, err := os.ReadFile(path)
	if err != nil {
		return f, fmt.Errorf("read MG368_FIXTURES: %w", err)
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("invalid MG368_FIXTURES JSON")
	}
	if f.ProbeImage == "" {
		return f, fmt.Errorf("probeImage is required (python3 and /bin/bash)")
	}
	return f, nil
}

func (s SFTPEndpoint) Address() string { return net.JoinHostPort(s.Host, strconv.Itoa(s.Port)) }
func (s SFTPEndpoint) CRHost() string {
	if s.Port == 22 {
		if strings.Contains(s.Host, ":") {
			return "[" + s.Host + "]"
		}
		return s.Host
	}
	return s.Address()
}
func (s SFTPEndpoint) Credentials() (string, string, error) {
	u, p := os.Getenv(s.UsernameEnv), os.Getenv(s.PasswordEnv)
	if u == "" || p == "" || s.HostKey == "" || s.Port < 1 || s.Port > 65535 || s.Host == "" {
		return "", "", fmt.Errorf("SFTP fixture needs host, port, pinned hostKey and populated credential environment references")
	}
	return u, p, nil
}

// networkPort converts a fixture/URL port into NetworkPolicy port space after
// bounding it to the valid TCP/UDP range (avoids gosec G115 false positives).
func networkPort(port int) int32 {
	ginkgo.GinkgoHelper()
	Expect(port >= 1 && port <= 65535).To(BeTrue(), "port out of range: %d", port)
	return int32(port) //nolint:gosec // G115: bounded to uint16 range above
}

func JobObject(ns, name string) *batchv1.Job {
	return &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}}
}

func credentialSecret(ns, name, user, password string) *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Data: map[string][]byte{"username": []byte(user), "password": []byte(password)}}
}
func ConflictOwner(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "foreign-policy-owner", Namespace: ns}}
}
func ImageStream(ns, name, image string) *imagev1.ImageStream {
	return &imagev1.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Spec: imagev1.ImageStreamSpec{Tags: []imagev1.TagReference{{Name: "test", From: &corev1.ObjectReference{Kind: "DockerImage", Name: image}, ImportPolicy: imagev1.TagImportPolicy{Scheduled: false}}}}}
}

// Script splits shell statements into bounded CR command-array entries. Each
// entry stays below the CRD's 256-byte item limit; the script is not interpolated
// into a local shell command.
func Script(lines ...string) []string {
	return append([]string{"/bin/bash", "-c", `eval "$(printf '%s\n' "$@")"`, "mg368"}, lines...)
}
func HoldScript(realGather bool) []string {
	// Bind IngressPort in the gather process tree so npListener()/ingress probes
	// work; CRI-O reaps listeners started via `oc exec`. Keep the accept loop on
	// the Python main thread (bash backgrounds it with &): a daemon thread dies
	// when the short-lived interpreter exits and the socket never stays up.
	// Avoid /proc/$$/comm: kubelet expands $$ → $ in command args (Go os.Expand).
	listen := fmt.Sprintf("python3 -c \"import socket;s=socket.socket();s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1);s.bind(('0.0.0.0',%d));s.listen(50);[s.accept()[0].close() for _ in iter(int,1)]\" &", IngressPort)
	lines := []string{"set -eu", listen, fmt.Sprintf("until test -f %s; do test $SECONDS -lt %d || exit 90; sleep %g; done", ReleaseFile, int(HoldTimeout.Seconds()), DefaultInterval.Seconds())}
	if realGather {
		lines = append(lines, "/usr/bin/gather")
	} else {
		lines = append(lines, "mkdir -p /must-gather", "printf 'mg368-fixture 192.0.2.123\\n' > /must-gather/fixture.log")
	}
	return Script(lines...)
}
func Request(ns, name string, retain, realGather bool) *mgv1.MustGather {
	return &mgv1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Spec: mgv1.MustGatherSpec{ServiceAccountName: serviceAccount, RetainResourcesOnCompletion: ptr.To(retain), GatherSpec: &mgv1.GatherSpec{Command: HoldScript(realGather)}}}
}
func WithUpload(mg *mgv1.MustGather, s SFTPEndpoint, secret string) {
	mg.Spec.UploadTarget = &mgv1.UploadTargetSpec{Type: mgv1.UploadTypeSFTP, SFTP: &mgv1.SFTPSpec{CaseID: mg.Namespace + "-" + mg.Name, Host: ptr.To(s.CRHost()), CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: secret}, InternalUser: ptr.To(false)}}
}
func ProbePod(ns, name, image, pvc string) *corev1.Pod {
	// Listen on IngressPort in the main process so controlHealthy works without
	// relying on an `oc exec`-spawned listener surviving the exec session.
	cmd := fmt.Sprintf(`import socket,threading,time
s=socket.socket(); s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1); s.bind(("0.0.0.0",%d)); s.listen(50)
def serve():
  while True:
    try:
      c,_=s.accept(); c.close()
    except Exception:
      return
threading.Thread(target=serve, daemon=True).start()
time.sleep(%g)`, IngressPort, CaseTimeout.Seconds())
	p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Spec: corev1.PodSpec{ServiceAccountName: serviceAccount, RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{Name: "probe", Image: image, Command: []string{"python3", "-c", cmd}, SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: ptr.To(false), RunAsNonRoot: ptr.To(true), Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}, SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}}}}}}
	if pvc != "" {
		p.Spec.Volumes = []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvc}}}}
		p.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{Name: "data", MountPath: "/data"}}
	}
	return p
}
func Grant(ns, name, identity string, ports ...int32) *netv1.NetworkPolicy {
	ps := []netv1.NetworkPolicyPort{}
	for _, p := range ports {
		ps = append(ps, netv1.NetworkPolicyPort{Protocol: ptr.To(corev1.ProtocolTCP), Port: ptr.To(intstr.FromInt32(p))})
	}
	return &netv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Spec: netv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{IdentityLabel: identity}}, PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeEgress}, Egress: []netv1.NetworkPolicyEgressRule{{Ports: ps}}}}
}
func LegacyJob(mg *mgv1.MustGather, image string) *batchv1.Job {
	return &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mg.Name, Namespace: mg.Namespace, OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(mg, mgv1.GroupVersion.WithKind("MustGather"))}}, Spec: batchv1.JobSpec{BackoffLimit: ptr.To(int32(3)), Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{ServiceAccountName: serviceAccount, RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{Name: "gather", Image: image, Command: HoldScript(false), VolumeMounts: []corev1.VolumeMount{{Name: "output", MountPath: "/must-gather"}}}}, Volumes: []corev1.Volume{{Name: "output", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}}}}}
}

func ocExec(ctx context.Context, _ *rest.Config, _ kubernetes.Interface, pod *corev1.Pod, container string, command ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()
	args := append([]string{"--namespace", pod.Namespace, "exec", pod.Name, "--container", container, "--"}, command...)
	cmd := exec.CommandContext(ctx, "oc", args...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err := cmd.Run()
	if err != nil {
		return out.String(), fmt.Errorf("exec %s/%s container %s failed: %w; %s", pod.Namespace, pod.Name, container, err, stderr.String())
	}
	return out.String(), nil
}

// TCP resolves separately so DNS failures cannot masquerade as a policy denial.
func npTCP(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, pod *corev1.Pod, container, host string, port int32) (bool, error) {
	script := `import socket,sys
host,port=sys.argv[1],int(sys.argv[2])
socket.getaddrinfo(host,port)
try:
 s=socket.create_connection((host,port),float(sys.argv[3]));s.close();print("allowed")
except (TimeoutError,ConnectionRefusedError,OSError):
 print("denied")`
	out, err := ocExec(ctx, cfg, cs, pod, container, "python3", "-c", script, host, fmt.Sprint(port), fmt.Sprint(ProbeTimeout.Seconds()))
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(out) {
	case "allowed":
		return true, nil
	case "denied":
		return false, nil
	default:
		return false, fmt.Errorf("unexpected TCP probe result")
	}
}

// DNS exercises both TCP and UDP to the Pod's configured cluster resolver.
func npDNS(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, pod *corev1.Pod) error {
	script := `import socket,struct,sys
server=next(line.split()[1] for line in open('/etc/resolv.conf') if line.startswith('nameserver'))
search=[word for line in open('/etc/resolv.conf') if line.startswith('search ') for word in line.split()[1:]]
domain=next((word.split('.svc.',1)[1] for word in search if '.svc.' in word),'cluster.local')
name=b''.join(bytes([len(s)])+s.encode() for s in ('kubernetes.default.svc.'+domain).split('.'))+b'\0'
query=struct.pack('!HHHHHH',1234,256,1,0,0,0)+name+struct.pack('!HH',1,1)
family=socket.AF_INET6 if ':' in server else socket.AF_INET
for kind in (socket.SOCK_DGRAM,socket.SOCK_STREAM):
 with socket.socket(family,kind) as s:
  s.settimeout(float(sys.argv[1]));s.connect((server,53))
  if kind==socket.SOCK_DGRAM:
   s.send(query);reply=s.recv(4096)
  else:
   s.sendall(struct.pack('!H',len(query))+query)
   def read(n):
    data=b''
    while len(data)<n:
     part=s.recv(n-len(data))
     if not part: raise RuntimeError('DNS stream closed')
     data+=part
    return data
   reply=read(struct.unpack('!H',read(2))[0])
  ident,flags,questions,answers=struct.unpack('!HHHH',reply[:8])
  assert ident==1234 and flags&32768 and flags&15==0 and answers>0
print('TCP and UDP DNS succeeded')`
	_, err := ocExec(ctx, cfg, cs, pod, "gather", "python3", "-c", script, fmt.Sprint(ProbeTimeout.Seconds()))
	return err
}

func npListener(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, pod *corev1.Pod, container string) error {
	// CRI-O reaps the entire `oc exec` process tree when the session ends, so a
	// listener cannot be spawned via exec. ProbePod and HoldScript already bind
	// IngressPort in the main container process; wait for that socket here.
	deadline := time.Now().Add(DefaultTimeout)
	for time.Now().Before(deadline) {
		ok, err := npTCP(ctx, cfg, cs, pod, container, "127.0.0.1", IngressPort)
		if err == nil && ok {
			return nil
		}
		time.Sleep(DefaultInterval)
	}
	return fmt.Errorf("listener on %s/%s port %d did not become ready", pod.Namespace, pod.Name, IngressPort)
}

func npObject(version, kind, ns, name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(version)
	u.SetKind(kind)
	u.SetNamespace(ns)
	u.SetName(name)
	return u
}

// WithSFTP uses a pinned fixture host key and a bounded connection. It never
// includes credentials in an error or logs the connection configuration.
func npWithSFTP(ctx context.Context, endpoint SFTPEndpoint, fn func(*sftp.Client) error) error {
	user, password, err := endpoint.Credentials()
	if err != nil {
		return err
	}
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(endpoint.HostKey))
	if err != nil {
		return fmt.Errorf("invalid pinned SFTP fixture host key")
	}
	c, err := (&net.Dialer{Timeout: ProbeTimeout}).DialContext(ctx, "tcp", endpoint.Address())
	if err != nil {
		return fmt.Errorf("SFTP fixture connection failed: %w", err)
	}
	defer c.Close()
	if err = c.SetDeadline(time.Now().Add(DefaultTimeout)); err != nil {
		return err
	}
	conn, chans, reqs, err := ssh.NewClientConn(c, endpoint.Address(), &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{ssh.Password(password)}, HostKeyCallback: ssh.FixedHostKey(key), Timeout: ProbeTimeout})
	if err != nil {
		return fmt.Errorf("SFTP fixture SSH handshake failed")
	}
	sshClient := ssh.NewClient(conn, chans, reqs)
	defer sshClient.Close()
	client, err := sftp.NewClient(sshClient)
	if err != nil {
		return fmt.Errorf("SFTP fixture subsystem failed")
	}
	defer client.Close()
	return fn(client)
}

func npArchives(ctx context.Context, endpoint SFTPEndpoint, caseID string, remove bool) (string, error) {
	var content string
	err := npWithSFTP(ctx, endpoint, func(c *sftp.Client) error {
		dir := endpoint.Directory
		if dir == "" {
			dir = "."
		}
		entries, err := c.ReadDir(dir)
		if err != nil {
			return err
		}
		found := false
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), caseID+"_") || !strings.HasSuffix(entry.Name(), ".tar.gz") {
				continue
			}
			found = true
			name := path.Join(dir, entry.Name())
			if remove {
				if err := c.Remove(name); err != nil {
					return err
				}
				continue
			}
			f, err := c.Open(name)
			if err != nil {
				return err
			}
			gz, err := gzip.NewReader(f)
			if err != nil {
				f.Close()
				return err
			}
			tr := tar.NewReader(io.LimitReader(gz, 64<<20))
			for {
				h, err := tr.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					gz.Close()
					f.Close()
					return err
				}
				if path.Base(h.Name) == "fixture.log" {
					b, err := io.ReadAll(io.LimitReader(tr, 1<<20))
					if err != nil {
						gz.Close()
						f.Close()
						return err
					}
					content += string(b)
				}
			}
			gz.Close()
			f.Close()
		}
		if !found && !remove {
			return fmt.Errorf("request archive not present yet")
		}
		return nil
	})
	return content, err
}

func npLogs(ctx context.Context, cs kubernetes.Interface, ns, pod, container string, since time.Time) (string, error) {
	b, err := cs.CoreV1().Pods(ns).GetLogs(pod, &corev1.PodLogOptions{Container: container, SinceTime: &metav1.Time{Time: since}}).DoRaw(ctx)
	return string(b), err
}

// registerNetworkPolicyTests nests MG-368 NetworkPolicy coverage inside the main
// Ordered "MustGather resource" suite so it reuses BeforeAll namespace, clients, and SA.
func registerNetworkPolicyTests() {
	ginkgo.Context("MG-368 NetworkPolicy", ginkgo.Label("MG-368", "NetworkPolicy"), func() {
		var e *npEnvironment
		ginkgo.BeforeEach(func() {
			if !setupComplete || ns == nil || adminClient == nil || adminRestConfig == nil {
				ginkgo.Skip("main e2e BeforeAll setup is required")
			}
			f, err := loadNPFixtures()
			Expect(err).NotTo(HaveOccurred())
			e = newNPEnvironment(f)
		})

		ginkgo.It("E2E-001 baseline protects default collection and survives reuse", func() {
			e.noProxy()
			ginkgo.By("running real default collection behind DNS/API-only rules")
			m := e.request("default", false, true)
			p := e.start(m)
			base := e.baseline()
			e.assertBaselineRules(base)
			Expect(e.job(m).Spec.Template.Spec.Containers).To(HaveLen(1))
			e.api(p)
			e.deny(p, e.control.Status.PodIP, IngressPort)
			Expect(npListener(e.ctx, e.cfg, e.cs, p, "gather")).To(Succeed())
			e.wait(func() error {
				ok, err := npTCP(e.ctx, e.cfg, e.cs, p, "gather", "127.0.0.1", IngressPort)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("managed listener not ready")
				}
				return nil
			})
			e.wait(func() error {
				ok, err := npTCP(e.ctx, e.cfg, e.cs, e.control, "probe", p.Status.PodIP, IngressPort)
				if err != nil {
					return err
				}
				if ok {
					return fmt.Errorf("unsolicited ingress allowed")
				}
				return nil
			})
			e.finish(m, p)
			ginkgo.By("completing a second collection without replacing the baseline")
			n := e.request("repeat", false, true)
			q := e.start(n)
			e.finish(n, q)
			Expect(e.baseline().UID).To(Equal(base.UID))
			Expect(e.baseline().Spec).To(Equal(base.Spec))
			e.noTemporary(m)
			e.noTemporary(n)
			e.health()
		})

		ginkgo.It("E2E-002 SFTP default/custom ports transfer and clean up", func() {
			e.noProxy()
			a, b := e.endpoints()
			for i, s := range []SFTPEndpoint{a, b} {
				ginkgo.By("transferring an archive with the request's exact SFTP port")
				m := e.upload(fmt.Sprintf("upload-%d", i), false, s)
				p := e.start(m, networkPort(s.Port))
				e.finish(m, p)
				e.archive(m, s)
				e.absentJob(m)
				e.noTemporary(m)
			}
			e.baseline()
			e.health()
		})

		ginkgo.It("E2E-003 concurrent uploads never inherit each other's ports", func() {
			e.noProxy()
			a, b := e.endpoints()
			m := e.upload("concurrent-a", false, a)
			p := e.start(m, networkPort(a.Port))
			n := e.upload("concurrent-b", false, b)
			q := e.start(n, networkPort(b.Port))
			ginkgo.By("probing both live endpoints from both managed Pods")
			e.allow(p, a.Host, networkPort(a.Port))
			e.denyStable(p, b.Host, networkPort(b.Port))
			e.allow(q, b.Host, networkPort(b.Port))
			e.denyStable(q, a.Host, networkPort(a.Port))
			baselineUID, policyUID := e.baseline().UID, e.policy(n).UID
			e.during(func() { e.finish(m, p) }, func() error { return e.tcpResult(q, b.Host, networkPort(b.Port), true) })
			e.archive(m, a)
			Expect(e.policy(n).UID).To(Equal(policyUID))
			Expect(e.pod(n).UID).To(Equal(q.UID))
			Expect(e.baseline().UID).To(Equal(baselineUID))
			e.denyStable(q, a.Host, networkPort(a.Port))
			e.finish(n, q)
			e.archive(n, b)
		})

		ginkgo.It("NEG-001 blocked custom collection fails without a permissive fallback", func() {
			e.noProxy()
			m := e.request("blocked", false, false)
			m.Spec.ImageStreamRef = e.customImage()
			// Hold first so control health, real Pod selection and denial are observed.
			m.Spec.GatherSpec.Command = Script("set -eu", fmt.Sprintf("until test -f %s; do test $SECONDS -lt %d || exit 90; sleep %g; done", ReleaseFile, int(HoldTimeout.Seconds()), DefaultInterval.Seconds()), fmt.Sprintf("timeout %g bash -c 'exec 3<>/dev/tcp/%s/%d' || exit 42", ProbeTimeout.Seconds(), e.control.Status.PodIP, IngressPort), "exit 43")
			p := e.start(m)
			e.deny(p, e.control.Status.PodIP, IngressPort)
			ginkgo.By("releasing the failing command through the Job retry budget")
			e.release(p)
			// Subsequent retry Pods have a fresh emptyDir; release each bounded fixture.
			e.waitFor(CollectionTimeout, func() error {
				pods := &corev1.PodList{}
				if err := e.c.List(e.ctx, pods, client.InNamespace(e.ns), client.MatchingLabels{"job-name": m.Name}); err != nil {
					return err
				}
				for i := range pods.Items {
					if pods.Items[i].Status.Phase == corev1.PodRunning {
						_, _ = ocExec(e.ctx, e.cfg, e.cs, &pods.Items[i], "gather", "touch", ReleaseFile)
					}
				}
				current := e.getRequest(m)
				if ptr.Deref(current.Status.Completed, false) && ptr.Deref(current.Status.Status, "") == "Failed" {
					return nil
				}
				return fmt.Errorf("waiting for terminal failed collection")
			})
			e.expectTerminalJob(m, false)
			e.noTemporary(m)
			e.absentJob(m)
			e.baseline()
			n := e.request("after-block", false, false)
			q := e.start(n)
			e.api(q)
			e.finish(n, q)
		})

		ginkgo.It("ERR-001 baseline conflicts fail closed then recover", func() {
			e.noProxy()
			ginkgo.By("preserving a foreign baseline until the fixture is removed")
			if e.exists(Grant(e.ns, BaselineName, "unused", 1)) {
				e.delete(e.baseline())
			}
			conflict := Grant(e.ns, BaselineName, "foreign", IngressPort)
			e.create(conflict)
			before := conflict.DeepCopy()
			m := e.request("conflict-foreign", false, false)
			e.createRequest(m)
			e.retryError(m, "baseline network policy")
			e.staysAbsent(m)
			Expect(e.getPolicy(conflict.Name).Spec).To(Equal(before.Spec))
			Expect(e.getPolicy(conflict.Name).UID).To(Equal(before.UID))
			e.delete(conflict)
			p := e.observe(m)
			e.finish(m, p)
		})

		ginkgo.It("ERR-002 foreign temporary policies survive reconcile and cleanup", func() {
			e.noProxy()
			a, b := e.endpoints()
			other := e.upload("independent", false, b)
			q := e.start(other, networkPort(b.Port))
			foreignOwner := ConflictOwner(e.ns)
			e.create(foreignOwner)
			m := e.upload("foreign-recover", false, a)
			foreign := Grant(e.ns, "must-gather-foreign-recover-sftp", "foreign", IngressPort)
			foreign.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(foreignOwner, corev1.SchemeGroupVersion.WithKind("ConfigMap"))}
			foreign.Labels = map[string]string{ManagerLabel: ManagerValue}
			e.create(foreign)
			snapshot := foreign.DeepCopy()
			e.createRequest(m)
			e.retryError(m, "not managed")
			e.staysAbsent(m)
			Expect(e.getPolicy(foreign.Name).Spec).To(Equal(snapshot.Spec))
			Expect(e.getPolicy(foreign.Name).UID).To(Equal(snapshot.UID))
			e.delete(foreign)
			p := e.observe(m, networkPort(a.Port))
			e.finish(m, p)
			e.archive(m, a)
			e.allow(q, b.Host, networkPort(b.Port))
			e.finish(other, q)
			e.archive(other, b)
		})

		ginkgo.It("NEG-002 active policy repair survives drift deletion and restart", ginkgo.Label("Disruptive"), func() {
			e.disruptive()
			e.noProxy()
			a, _ := e.endpoints()
			m := e.upload("repair", false, a)
			p := e.start(m, networkPort(a.Port))
			id := p.UID
			ginkgo.By("repairing deleted and corrupted owned policies")
			e.delete(e.policy(m))
			e.expectPorts(m, networkPort(a.Port))
			e.allow(p, a.Host, networkPort(a.Port))
			policy := e.policy(m)
			bad := Grant(e.ns, policy.Name, m.Name, IngressPort)
			policy.Spec = bad.Spec
			Expect(e.c.Update(e.ctx, policy)).To(Succeed())
			e.expectPorts(m, networkPort(a.Port))
			e.deny(p, e.control.Status.PodIP, IngressPort)
			ginkgo.By("keeping traffic alive while the dedicated operator Pod restarts")
			e.during(func() { e.restartOperator() }, func() error { return e.tcpResult(p, a.Host, networkPort(a.Port), true) })
			Expect(e.pod(m).UID).To(Equal(id))
			ginkgo.By("recreating the ownerless baseline on an owned-policy event")
			base := e.baseline()
			e.delete(base)
			e.delete(e.policy(m))
			e.expectPorts(m, networkPort(a.Port))
			e.wait(func() error {
				if !e.exists(Grant(e.ns, BaselineName, "", 1)) {
					return fmt.Errorf("baseline missing")
				}
				return nil
			})
			Expect(e.baseline().UID).NotTo(Equal(base.UID))
			e.finish(m, p)
			e.archive(m, a)
			e.staysAbsent(m)
		})

		ginkgo.It("E2E-004 proxy explicit ports with real transfer", ginkgo.Label("Disruptive"), func() {
			e.disruptive()
			e.proxyFixtures()
			a, _ := e.endpoints()
			f := e.f.Proxies
			rows := []struct {
				http, https string
				ports       []int32
				sftp        *SFTPEndpoint
			}{
				{proxyURL("http", f.HTTPHost, f.HTTPPort), proxyURL("https", f.HTTPSHost, f.HTTPSPort), []int32{networkPort(f.HTTPPort), networkPort(f.HTTPSPort)}, nil},
				{proxyURL("http", f.HTTPHost, f.HTTPPort), "", []int32{networkPort(a.Port), networkPort(f.HTTPPort)}, &a},
			}
			for i, row := range rows {
				ginkgo.By(fmt.Sprintf("observing proxy configuration row %d", i+1))
				e.setProxy(row.http, row.https, "")
				m := e.request(fmt.Sprintf("proxy-%d", i), false, false)
				if row.sftp != nil {
					m = e.upload(m.Name, false, *row.sftp)
				}
				started := time.Now()
				p := e.start(m, row.ports...)
				for _, raw := range []string{row.http, row.https} {
					if raw == "" {
						continue
					}
					endpoint, err := url.Parse(raw)
					Expect(err).NotTo(HaveOccurred())
					port := int64(3128)
					if endpoint.Scheme == "https" {
						port = 3129
					}
					if endpoint.Port() != "" {
						port, err = strconv.ParseInt(endpoint.Port(), 10, 32)
						Expect(err).NotTo(HaveOccurred())
					}
					e.allow(p, endpoint.Hostname(), networkPort(int(port)))
				}
				e.deny(p, e.control.Status.PodIP, IngressPort)
				if row.sftp == nil {
					Expect(e.job(m).Spec.Template.Spec.Containers).To(HaveLen(1))
					e.api(p)
					e.finish(m, p)
				} else {
					e.finish(m, p)
					e.archive(m, *row.sftp)
					e.proxyLog(started, row.sftp.Host)
				}
			}
			e.restore()
			e.health()
		})

		ginkgo.It("E2E-005 retention preserves policy until owner garbage collection", func() {
			e.noProxy()
			a, _ := e.endpoints()
			m := e.upload("retained", true, a)
			p := e.start(m, networkPort(a.Port))
			policy, base := e.policy(m), e.baseline()
			e.finish(m, p)
			e.archive(m, a)
			ginkgo.By("keeping retained Job Pod and policy while the CR exists")
			Consistently(func() bool { return e.exists(m) && e.exists(p) && e.exists(policy) && e.exists(e.jobObject(m)) }, ObservationWindow, DefaultInterval).Should(BeTrue())
			foreign := Grant(e.ns, "administrator", "other", IngressPort)
			e.create(foreign)
			e.delete(m)
			e.gone(p)
			e.gone(policy)
			e.gone(e.jobObject(m))
			Expect(e.baseline().UID).To(Equal(base.UID))
			Expect(e.getPolicy(foreign.Name).UID).To(Equal(foreign.UID))
		})

		ginkgo.It("E2E-008 legacy Jobs keep their network posture", ginkgo.Label("Disruptive"), func() {
			e.disruptive()
			e.noProxy()
			e.replicas(0)
			m := e.request("legacy", false, false)
			e.createRequest(m)
			legacy := LegacyJob(m, e.f.ProbeImage)
			e.create(legacy)
			p := e.pod(m)
			e.allow(p, e.control.Status.PodIP, IngressPort)
			original := legacy.Spec.Template.DeepCopy()
			ginkgo.By("resuming reconciliation beside a new restricted Job")
			e.replicas(1)
			e.health()
			n := e.request("new", false, false)
			q := e.start(n)
			e.deny(q, e.control.Status.PodIP, IngressPort)
			e.allow(p, e.control.Status.PodIP, IngressPort)
			Expect(e.job(m).Spec.Template).To(Equal(*original))
			Expect(e.pod(m).UID).To(Equal(p.UID))
			e.noTemporary(m)
			e.finish(m, p)
			e.finish(n, q)
		})

		ginkgo.It("E2E-009 hashed name boundaries keep stable identities", func() {
			e.noProxy()
			a, _ := e.endpoints()
			ginkgo.By("using a hashed identity for a name exceeding the label limit")
			long := strings.Repeat("b", 64)
			m := e.upload(long, false, a)
			e.createRequest(m)
			policy := e.policy(m)
			identity := policy.Spec.PodSelector.MatchLabels[IdentityLabel]
			Expect(validation.IsValidLabelValue(identity)).To(BeEmpty())
			Expect(validation.IsDNS1123Subdomain(policy.Name)).To(BeEmpty())
			Expect(identity).NotTo(Equal(long))
			Expect(identity).To(HavePrefix("mg_"))
			Expect(policy.Name).To(HavePrefix("must-gather-long-"))
			Expect(policy.Name).To(HaveSuffix("-sftp"))
			Expect(policy.Name).NotTo(ContainSubstring("_"), "hashed upload policy names must be DNS-1123 (underscore mapped to hyphen)")
			Expect(policy.Name).To(Equal("must-gather-long-" + strings.ReplaceAll(identity, "_", "-") + "-sftp"))
			e.wait(func() error {
				j := e.jobObject(m)
				if err := e.c.Get(e.ctx, client.ObjectKeyFromObject(j), j); err == nil {
					return nil
				}
				current := e.getRequest(m)
				for _, c := range current.Status.Conditions {
					if c.Type == "ReconcileError" && strings.Contains(c.Message, "Invalid value") && strings.Contains(c.Message, "63") {
						ginkgo.AddReportEntry("native Job admission limit", fmt.Sprintf("length=%d; policy admission passed; collection not covered", len(long)))
						return nil
					}
				}
				return fmt.Errorf("waiting for Job or explicit native name admission error")
			})
			if e.exists(e.jobObject(m)) {
				q := e.pod(m)
				e.finish(m, q)
				e.archive(m, a)
			}
			e.delete(m)
			e.gone(policy)
		})

		ginkgo.It("NEG-003 additive grants stay administrator controlled", func() {
			e.noProxy()
			m := e.request("additive", false, false)
			p := e.start(m)
			e.deny(p, e.control.Status.PodIP, IngressPort)
			grant := Grant(e.ns, "administrator", p.Labels[IdentityLabel], IngressPort)
			e.create(grant)
			e.allow(p, e.control.Status.PodIP, IngressPort)
			e.delete(grant)
			e.deny(p, e.control.Status.PodIP, IngressPort)
			e.finish(m, p)
			e.baseline()
		})

		ginkgo.It("ERR-003 invalid SFTP input fails terminally", func() {
			e.noProxy()
			a, _ := e.endpoints()
			m := e.upload("invalid-port", false, a)
			m.Spec.UploadTarget.SFTP.Host = ptr.To(net.JoinHostPort(a.Host, "65536"))
			e.createRequest(m)
			e.wait(func() error {
				current := e.getRequest(m)
				if !ptr.Deref(current.Status.Completed, false) || ptr.Deref(current.Status.Status, "") != "Failed" {
					return fmt.Errorf("waiting for terminal validation")
				}
				for _, c := range current.Status.Conditions {
					if c.Type == "ReconcileError" && c.Reason == "ValidationFailed" {
						return nil
					}
				}
				return fmt.Errorf("ValidationFailed condition missing")
			})
			e.staysAbsent(m)
			e.noTemporary(m)
		})

		ginkgo.It("ERR-004 invalid proxy configuration is sanitized and retryable", ginkgo.Label("Disruptive"), func() {
			e.disruptive()
			marker := "mg368-synthetic-secret"
			e.setProxy("http://user:"+marker+"@invalid.example:65536", "", "")
			m := e.request("invalid-proxy", false, false)
			e.createRequest(m)
			e.retryError(m, "HTTP_PROXY")
			e.staysAbsent(m)
			e.noTemporary(m)
			current := e.getRequest(m)
			Expect(fmt.Sprint(current.Status)).NotTo(ContainSubstring(marker))
			events := &corev1.EventList{}
			Expect(e.c.List(e.ctx, events, client.InNamespace(e.ns))).To(Succeed())
			for _, event := range events.Items {
				if event.InvolvedObject.UID == m.UID {
					Expect(event.Message).NotTo(ContainSubstring(marker))
				}
			}
			e.setProxy("", "", "")
			p := e.observe(m)
			e.finish(m, p)
			e.restore()
			e.health()
		})

	})
}

type npEnvironment struct {
	ctx          context.Context
	f            Fixtures
	c            client.Client
	cs           kubernetes.Interface
	cfg          *rest.Config
	ns           string
	control      *corev1.Pod
	observer     *corev1.Pod
	restores     []func()
	savedFields  map[string]bool
	observations map[string]*npJobObservation
}
type npJobObservation struct {
	sync.Mutex
	succeeded, failed bool
	firstChecked      bool
	firstPolicyError  error
}

func loadNPFixtures() (Fixtures, error) {
	if path := os.Getenv("MG368_FIXTURES"); path != "" {
		return LoadFixtures(path)
	}
	f := Fixtures{
		OperatorNamespace: operatorNamespace,
		Deployment:        operatorDeployment,
		GatherRole:        "must-gather-admin",
		ProbeImage:        os.Getenv("MG368_PROBE_IMAGE"),
		IsolatedOperator:  false,
	}
	if f.ProbeImage == "" {
		f.ProbeImage = defaultMustGatherImageFromOperator()
	}
	if f.ProbeImage == "" {
		return f, fmt.Errorf("set MG368_FIXTURES or MG368_PROBE_IMAGE to a python3+/bin/bash image; see test/e2e/NETWORKPOLICY.md")
	}
	if sub := os.Getenv("MG368_SUBSCRIPTION"); sub != "" {
		f.Subscription = sub
	}
	return f, nil
}

func defaultMustGatherImageFromOperator() string {
	d := &appsv1.Deployment{}
	if err := adminClient.Get(testCtx, client.ObjectKey{Namespace: operatorNamespace, Name: operatorDeployment}, d); err != nil {
		return ""
	}
	for _, c := range d.Spec.Template.Spec.Containers {
		for _, e := range c.Env {
			if e.Name == "DEFAULT_MUST_GATHER_IMAGE" && e.Value != "" {
				return e.Value
			}
		}
	}
	return ""
}

func newNPEnvironment(f Fixtures) *npEnvironment {
	ctx, cancel := context.WithTimeout(context.Background(), CaseTimeout)
	ginkgo.DeferCleanup(cancel)
	cs, err := kubernetes.NewForConfig(adminRestConfig)
	Expect(err).NotTo(HaveOccurred())
	e := &npEnvironment{
		ctx: ctx, f: f, c: adminClient, cs: cs, cfg: adminRestConfig,
		ns: ns.Name, observations: map[string]*npJobObservation{}, savedFields: map[string]bool{},
	}
	e.health()
	_, err = exec.LookPath("oc")
	Expect(err).NotTo(HaveOccurred(), "oc is required for Pod exec and must use the suite KUBECONFIG")
	e.checkPermissions()
	suffix := strconv.FormatInt(time.Now().UnixNano()%100000, 10)
	e.control = ProbePod(e.ns, "np-control-"+suffix, f.ProbeImage, "")
	e.create(e.control)
	e.ready(e.control)
	e.observer = ProbePod(e.ns, "np-observer-"+suffix, f.ProbeImage, "")
	e.create(e.observer)
	e.ready(e.observer)
	Expect(npListener(ctx, adminRestConfig, cs, e.control, "probe")).To(Succeed())
	e.controlHealthy(e.control.Status.PodIP, IngressPort)
	return e
}

func (e *npEnvironment) wait(fn func() error) { e.waitFor(DefaultTimeout, fn) }
func (e *npEnvironment) waitFor(timeout time.Duration, fn func() error) {
	ginkgo.GinkgoHelper()
	Eventually(fn, timeout, DefaultInterval).Should(Succeed())
}
func (e *npEnvironment) create(obj client.Object) {
	ginkgo.GinkgoHelper()
	Expect(e.c.Create(e.ctx, obj)).To(Succeed())
	snapshot := obj.DeepCopyObject().(client.Object)
	ginkgo.DeferCleanup(func() {
		e.restore()
		ctx, cancel := context.WithTimeout(context.Background(), CleanupTimeout)
		defer cancel()
		err := e.c.Delete(ctx, snapshot, client.Preconditions{UID: ptr.To(snapshot.GetUID())})
		if !apierrors.IsNotFound(err) && !apierrors.IsConflict(err) {
			Expect(err).NotTo(HaveOccurred())
		}
		if !apierrors.IsConflict(err) {
			Eventually(func() bool { return apierrors.IsNotFound(e.c.Get(ctx, client.ObjectKeyFromObject(snapshot), snapshot)) }, CleanupTimeout, DefaultInterval).Should(BeTrue(), "fixture cleanup did not finish")
		}
	})
}
func (e *npEnvironment) exists(obj client.Object) bool {
	err := e.c.Get(e.ctx, client.ObjectKeyFromObject(obj), obj)
	Expect(err == nil || apierrors.IsNotFound(err)).To(BeTrue(), "unexpected lookup error: %v", err)
	return err == nil
}
func (e *npEnvironment) gone(obj client.Object) {
	e.waitFor(CleanupTimeout, func() error {
		err := e.c.Get(e.ctx, client.ObjectKeyFromObject(obj), obj)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("resource %T %s still exists", obj, obj.GetName())
	})
}
func (e *npEnvironment) delete(obj client.Object) {
	ginkgo.GinkgoHelper()
	err := e.c.Delete(e.ctx, obj, client.Preconditions{UID: ptr.To(obj.GetUID())})
	if !apierrors.IsNotFound(err) {
		Expect(err).NotTo(HaveOccurred())
	}
	e.gone(obj)
}
func (e *npEnvironment) ready(p *corev1.Pod) {
	e.wait(func() error {
		if err := e.c.Get(e.ctx, client.ObjectKeyFromObject(p), p); err != nil {
			return err
		}
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				return nil
			}
		}
		return fmt.Errorf("Pod %s not Ready: %s", p.Name, p.Status.Phase)
	})
}
func (e *npEnvironment) exec(p *corev1.Pod, container string, cmd ...string) string {
	ginkgo.GinkgoHelper()
	out, err := ocExec(e.ctx, e.cfg, e.cs, p, container, cmd...)
	Expect(err).NotTo(HaveOccurred())
	return out
}
func (e *npEnvironment) request(name string, retain, realGather bool) *mgv1.MustGather {
	return Request(e.ns, name, retain, realGather)
}
func (e *npEnvironment) endpoints() (SFTPEndpoint, SFTPEndpoint) {
	if len(e.f.SFTP) < 2 {
		ginkgo.Skip("two controlled SFTP fixtures (22 and a distinct custom port) are required")
	}
	a, b := e.f.SFTP[0], e.f.SFTP[1]
	Expect(a.Port).To(Equal(22))
	Expect(b.Port).NotTo(Equal(a.Port))
	for _, s := range []SFTPEndpoint{a, b} {
		_, _, err := s.Credentials()
		Expect(err).NotTo(HaveOccurred())
	}
	return a, b
}
func (e *npEnvironment) upload(name string, retain bool, s SFTPEndpoint) *mgv1.MustGather {
	user, password, err := s.Credentials()
	Expect(err).NotTo(HaveOccurred())
	secret := "credentials-" + strconv.Itoa(s.Port)
	if !e.exists(credentialSecret(e.ns, secret, "", "")) {
		e.create(credentialSecret(e.ns, secret, user, password))
	}
	m := e.request(name, retain, false)
	WithUpload(m, s, secret)
	ginkgo.DeferCleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
		defer cancel()
		_, err := npArchives(ctx, s, m.Spec.UploadTarget.SFTP.CaseID, true)
		Expect(err).NotTo(HaveOccurred(), "remove fixture archives")
	})
	return m
}
func (e *npEnvironment) watchJob(m *mgv1.MustGather) {
	requireTemporary := m.Spec.UploadTarget != nil
	for _, c := range e.deployment().Spec.Template.Spec.Containers {
		for _, v := range c.Env {
			if (v.Name == "HTTP_PROXY" || v.Name == "HTTPS_PROXY") && v.Value != "" {
				requireTemporary = true
			}
		}
	}
	ctx := e.ctx
	watch, err := e.cs.BatchV1().Jobs(e.ns).Watch(ctx, metav1.ListOptions{FieldSelector: "metadata.name=" + m.Name})
	Expect(err).NotTo(HaveOccurred())
	ginkgo.DeferCleanup(watch.Stop)
	o := &npJobObservation{}
	e.observations[m.Name] = o
	go func() {
		for event := range watch.ResultChan() {
			if j, ok := event.Object.(*batchv1.Job); ok {
				o.Lock()
				if !o.firstChecked {
					o.firstChecked = true
					if j.Spec.Template.Labels[NetworkLabel] == "default" {
						baseline := &netv1.NetworkPolicy{}
						o.firstPolicyError = e.c.Get(ctx, client.ObjectKey{Namespace: j.Namespace, Name: BaselineName}, baseline)
						if o.firstPolicyError == nil && requireTemporary {
							list := &netv1.NetworkPolicyList{}
							o.firstPolicyError = e.c.List(ctx, list, client.InNamespace(j.Namespace))
							found := false
							owner := metav1.GetControllerOf(j)
							for _, policy := range list.Items {
								if po := metav1.GetControllerOf(&policy); po != nil && owner != nil && po.UID == owner.UID {
									found = true
								}
							}
							if !found && o.firstPolicyError == nil {
								o.firstPolicyError = fmt.Errorf("temporary policy absent at first Job observation")
							}
						}
					}
				}
				o.succeeded = o.succeeded || j.Status.Succeeded > 0
				o.failed = o.failed || j.Status.Failed > ptr.Deref(j.Spec.BackoffLimit, int32(3))
				for _, c := range j.Status.Conditions {
					if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
						o.failed = true
					}
				}
				o.Unlock()
			}
		}
	}()
}
func (e *npEnvironment) createRequest(m *mgv1.MustGather) { e.watchJob(m); e.create(m) }
func (e *npEnvironment) jobObject(m *mgv1.MustGather) *batchv1.Job {
	return JobObject(m.Namespace, m.Name)
}
func (e *npEnvironment) job(m *mgv1.MustGather) *batchv1.Job {
	j := e.jobObject(m)
	e.wait(func() error { return e.c.Get(e.ctx, client.ObjectKeyFromObject(j), j) })
	return j
}
func (e *npEnvironment) pod(m *mgv1.MustGather) *corev1.Pod {
	j := e.job(m)
	var pod corev1.Pod
	e.wait(func() error {
		list := &corev1.PodList{}
		if err := e.c.List(e.ctx, list, client.InNamespace(e.ns), client.MatchingLabels{"job-name": m.Name}); err != nil {
			return err
		}
		for _, p := range list.Items {
			if metav1.IsControlledBy(&p, j) && (p.Status.Phase == corev1.PodRunning || p.Status.Phase == corev1.PodSucceeded) {
				pod = p
				return nil
			}
		}
		return fmt.Errorf("managed Pod not running")
	})
	return &pod
}
func (e *npEnvironment) start(m *mgv1.MustGather, ports ...int32) *corev1.Pod {
	e.createRequest(m)
	return e.observe(m, ports...)
}
func (e *npEnvironment) observe(m *mgv1.MustGather, ports ...int32) *corev1.Pod {
	j := e.job(m)
	Expect(metav1.IsControlledBy(j, m)).To(BeTrue())
	e.baseline()
	if len(ports) > 0 {
		e.expectPorts(m, ports...)
	} else {
		e.noTemporary(m)
	}
	p := e.pod(m)
	Expect(p.Labels[NetworkLabel]).To(Equal("default"))
	Expect(p.Labels[IdentityLabel]).NotTo(BeEmpty())
	Expect(p.Labels[IdentityLabel]).To(Equal(j.Spec.Template.Labels[IdentityLabel]))
	if len(ports) > 0 {
		Expect(e.policy(m).Spec.PodSelector.MatchLabels).To(Equal(map[string]string{IdentityLabel: p.Labels[IdentityLabel]}))
	}
	return p
}
func (e *npEnvironment) getRequest(m *mgv1.MustGather) *mgv1.MustGather {
	current := &mgv1.MustGather{}
	Expect(e.c.Get(e.ctx, client.ObjectKeyFromObject(m), current)).To(Succeed())
	if current.Status == nil {
		current.Status = &mgv1.MustGatherStatus{}
	}
	return current
}
func (e *npEnvironment) release(p *corev1.Pod) {
	for _, c := range p.Spec.Containers {
		if c.Name == "gather" {
			e.exec(p, "gather", "touch", ReleaseFile)
			return
		}
	}
}
func (e *npEnvironment) finish(m *mgv1.MustGather, p *corev1.Pod) {
	ginkgo.GinkgoHelper()
	e.release(p)
	e.waitFor(CollectionTimeout, func() error {
		current := e.getRequest(m)
		if !ptr.Deref(current.Status.Completed, false) {
			return fmt.Errorf("request not completed")
		}
		if ptr.Deref(current.Status.Status, "") != "Completed" {
			return fmt.Errorf("request terminal status is %s", ptr.Deref(current.Status.Status, ""))
		}
		return nil
	})
	e.expectTerminalJob(m, true)
	if !ptr.Deref(m.Spec.RetainResourcesOnCompletion, false) {
		e.absentJob(m)
		e.noTemporary(m)
	}
}
func (e *npEnvironment) expectTerminalJob(m *mgv1.MustGather, success bool) {
	o := e.observations[m.Name]
	e.wait(func() error {
		o.Lock()
		defer o.Unlock()
		if o.firstPolicyError != nil {
			return o.firstPolicyError
		}
		if success && o.succeeded || !success && o.failed {
			return nil
		}
		return fmt.Errorf("terminal Job evidence not observed")
	})
}
func (e *npEnvironment) absentJob(m *mgv1.MustGather) {
	e.gone(e.jobObject(m))
	e.waitFor(CleanupTimeout, func() error {
		pods := &corev1.PodList{}
		if err := e.c.List(e.ctx, pods, client.InNamespace(m.Namespace), client.MatchingLabels{"job-name": m.Name}); err != nil {
			return err
		}
		if len(pods.Items) != 0 {
			return fmt.Errorf("request Pods remain after Job cleanup")
		}
		return nil
	})
}
func (e *npEnvironment) staysAbsent(m *mgv1.MustGather) {
	Consistently(func() bool { return e.exists(e.jobObject(m)) }, ObservationWindow, DefaultInterval).Should(BeFalse())
}
func (e *npEnvironment) getPolicy(name string) *netv1.NetworkPolicy {
	p := &netv1.NetworkPolicy{}
	Expect(e.c.Get(e.ctx, client.ObjectKey{Namespace: e.ns, Name: name}, p)).To(Succeed())
	return p
}
func (e *npEnvironment) policy(m *mgv1.MustGather) *netv1.NetworkPolicy {
	var result netv1.NetworkPolicy
	e.wait(func() error {
		list := &netv1.NetworkPolicyList{}
		if err := e.c.List(e.ctx, list, client.InNamespace(e.ns)); err != nil {
			return err
		}
		matches := 0
		for _, p := range list.Items {
			if metav1.IsControlledBy(&p, m) {
				result = p
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("expected one owned policy, got %d", matches)
		}
		return nil
	})
	return &result
}
func (e *npEnvironment) noTemporary(m *mgv1.MustGather) {
	e.wait(func() error {
		list := &netv1.NetworkPolicyList{}
		if err := e.c.List(e.ctx, list, client.InNamespace(e.ns)); err != nil {
			return err
		}
		for _, p := range list.Items {
			if metav1.IsControlledBy(&p, m) {
				return fmt.Errorf("temporary policy remains")
			}
		}
		return nil
	})
}
func (e *npEnvironment) expectPorts(m *mgv1.MustGather, ports ...int32) {
	want := uniquePorts(ports)
	e.wait(func() error {
		p := e.policy(m)
		if p.Labels[ManagerLabel] != ManagerValue {
			return fmt.Errorf("missing managed provenance")
		}
		owner := metav1.GetControllerOf(p)
		if owner == nil || owner.Kind != "MustGather" || owner.APIVersion != mgv1.GroupVersion.String() || len(p.Spec.PodSelector.MatchExpressions) != 0 {
			return fmt.Errorf("unexpected policy owner or selector")
		}
		if !reflect.DeepEqual(p.Spec.PolicyTypes, []netv1.PolicyType{netv1.PolicyTypeEgress}) || len(p.Spec.Ingress) != 0 || len(p.Spec.Egress) != 1 || len(p.Spec.Egress[0].To) != 0 {
			return fmt.Errorf("unexpected temporary policy rules")
		}
		actual := []int32{}
		for _, port := range p.Spec.Egress[0].Ports {
			if port.Port == nil || port.Port.Type != 0 || port.EndPort != nil || ptr.Deref(port.Protocol, corev1.ProtocolTCP) != corev1.ProtocolTCP {
				return fmt.Errorf("invalid TCP grant")
			}
			actual = append(actual, port.Port.IntVal)
		}
		if !reflect.DeepEqual(actual, want) {
			return fmt.Errorf("policy ports %v, expected %v", actual, want)
		}
		return nil
	})
}
func uniquePorts(ports []int32) []int32 {
	seen := map[int32]bool{}
	out := []int32{}
	for _, p := range ports {
		if !seen[p] {
			out = append(out, p)
			seen[p] = true
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
func (e *npEnvironment) baseline() *netv1.NetworkPolicy {
	p := e.getPolicy(BaselineName)
	Expect(p.OwnerReferences).To(BeEmpty())
	Expect(p.Labels[ManagerLabel]).To(Equal(ManagerValue))
	Expect(p.Spec.PodSelector.MatchLabels).To(Equal(map[string]string{NetworkLabel: "default"}))
	managed := &netv1.NetworkPolicyList{}
	Expect(e.c.List(e.ctx, managed, client.InNamespace(e.ns), client.MatchingLabels{ManagerLabel: ManagerValue})).To(Succeed())
	ownerless := 0
	for _, policy := range managed.Items {
		if len(policy.OwnerReferences) == 0 {
			ownerless++
		}
	}
	Expect(ownerless).To(Equal(1), "one persistent managed baseline per namespace")
	return p
}
func (e *npEnvironment) assertBaselineRules(p *netv1.NetworkPolicy) {
	Expect(p.Spec.PodSelector.MatchExpressions).To(BeEmpty())
	Expect(p.Spec.PolicyTypes).To(ConsistOf(netv1.PolicyTypeIngress, netv1.PolicyTypeEgress))
	Expect(p.Spec.Ingress).To(BeEmpty())
	Expect(p.Spec.Egress).To(HaveLen(2))
	dns, api := p.Spec.Egress[0], p.Spec.Egress[1]
	Expect(dns.To).To(HaveLen(1))
	Expect(dns.To[0].IPBlock).To(BeNil())
	Expect(dns.To[0].NamespaceSelector).NotTo(BeNil())
	Expect(dns.To[0].PodSelector).NotTo(BeNil())
	Expect(dns.To[0].NamespaceSelector.MatchLabels).To(Equal(map[string]string{"kubernetes.io/metadata.name": "openshift-dns"}))
	Expect(dns.To[0].PodSelector.MatchLabels).To(Equal(map[string]string{"dns.operator.openshift.io/daemonset-dns": "default"}))
	pairs := []string{}
	for _, port := range dns.Ports {
		Expect(port.Port).NotTo(BeNil())
		pairs = append(pairs, fmt.Sprintf("%s/%d", ptr.Deref(port.Protocol, corev1.ProtocolTCP), port.Port.IntVal))
	}
	Expect(pairs).To(ConsistOf("TCP/53", "UDP/53", "TCP/5353", "UDP/5353"))
	Expect(api.To).To(BeEmpty())
	Expect(api.Ports).To(HaveLen(1))
	Expect(api.Ports[0].Port.IntVal).To(Equal(int32(6443)))
	Expect(ptr.Deref(api.Ports[0].Protocol, corev1.ProtocolTCP)).To(Equal(corev1.ProtocolTCP))
}
func (e *npEnvironment) tcpResult(p *corev1.Pod, host string, port int32, allow bool) error {
	container := "gather"
	for _, c := range p.Spec.Containers {
		if c.Name == "probe" {
			container = "probe"
		}
	}
	ok, err := npTCP(e.ctx, e.cfg, e.cs, p, container, host, port)
	if err != nil {
		return err
	}
	if ok != allow {
		return fmt.Errorf("Pod %s port %d allowed=%t expected=%t", p.Name, port, ok, allow)
	}
	return nil
}
func (e *npEnvironment) controlHealthy(host string, port int32) {
	e.wait(func() error { return e.tcpResult(e.observer, host, port, true) })
}
func (e *npEnvironment) allow(p *corev1.Pod, host string, port int32) {
	e.controlHealthy(host, port)
	e.wait(func() error { return e.tcpResult(p, host, port, true) })
}
func (e *npEnvironment) deny(p *corev1.Pod, host string, port int32) {
	e.controlHealthy(host, port)
	e.wait(func() error { return e.tcpResult(p, host, port, false) })
}
func (e *npEnvironment) denyStable(p *corev1.Pod, host string, port int32) {
	e.deny(p, host, port)
	Consistently(func() error { return e.tcpResult(p, host, port, false) }, ObservationWindow, DefaultInterval).Should(Succeed())
}
func (e *npEnvironment) api(p *corev1.Pod) {
	Expect(npDNS(e.ctx, e.cfg, e.cs, p)).To(Succeed())
	out := e.exec(p, "gather", "python3", "-c", `import socket,ssl,urllib.request,sys
socket.getaddrinfo('kubernetes.default.svc',443)
base='/var/run/secrets/kubernetes.io/serviceaccount/'
req=urllib.request.Request('https://kubernetes.default.svc/api/v1/namespaces',headers={'Authorization':'Bearer '+open(base+'token').read()})
ctx=ssl.create_default_context(cafile=base+'ca.crt')
with urllib.request.urlopen(req,context=ctx,timeout=float(sys.argv[1])) as r: print(r.status)`, fmt.Sprint(ProbeTimeout.Seconds()))
	Expect(strings.TrimSpace(out)).To(Equal("200"))
}
func (e *npEnvironment) archive(m *mgv1.MustGather, s SFTPEndpoint) {
	e.wait(func() error {
		content, err := npArchives(e.ctx, s, m.Spec.UploadTarget.SFTP.CaseID, false)
		if err != nil {
			return err
		}
		if !strings.Contains(content, "mg368-fixture") {
			return fmt.Errorf("archive fixture output missing")
		}
		return nil
	})
}
func (e *npEnvironment) retryError(m *mgv1.MustGather, needle string) {
	e.wait(func() error {
		current := e.getRequest(m)
		if ptr.Deref(current.Status.Completed, false) {
			return fmt.Errorf("retryable request became terminal")
		}
		for _, c := range current.Status.Conditions {
			if c.Type == "ReconcileError" && strings.Contains(c.Message, needle) {
				return nil
			}
		}
		return fmt.Errorf("waiting for retryable error containing %s", needle)
	})
}
func (e *npEnvironment) during(action func(), probe func() error) {
	Expect(probe()).To(Succeed())
	ctx, cancel := context.WithCancel(e.ctx)
	var wg sync.WaitGroup
	var failures []error
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(DefaultInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := probe(); err != nil {
					failures = append(failures, err)
				}
			}
		}
	}()
	defer func() { cancel(); wg.Wait(); Expect(failures).To(BeEmpty(), "traffic failed during transition") }()
	action()
	Expect(probe()).To(Succeed())
}

func (e *npEnvironment) deployment() *appsv1.Deployment {
	d := &appsv1.Deployment{}
	Expect(e.c.Get(e.ctx, client.ObjectKey{Namespace: e.f.OperatorNamespace, Name: e.f.Deployment}, d)).To(Succeed())
	return d
}
func (e *npEnvironment) subscription() *unstructured.Unstructured {
	if e.f.Subscription == "" {
		ginkgo.Skip("OLM Subscription name is required in MG368_FIXTURES")
	}
	s := npObject("operators.coreos.com/v1alpha1", "Subscription", e.f.OperatorNamespace, e.f.Subscription)
	Expect(e.c.Get(e.ctx, client.ObjectKeyFromObject(s), s)).To(Succeed())
	return s
}
func (e *npEnvironment) csv() *unstructured.Unstructured {
	s := e.subscription()
	name, _, err := unstructured.NestedString(s.Object, "status", "installedCSV")
	Expect(err).NotTo(HaveOccurred())
	Expect(name).NotTo(BeEmpty())
	csv := npObject("operators.coreos.com/v1alpha1", "ClusterServiceVersion", e.f.OperatorNamespace, name)
	Expect(e.c.Get(e.ctx, client.ObjectKeyFromObject(csv), csv)).To(Succeed())
	return csv
}
func (e *npEnvironment) health() {
	e.wait(func() error {
		d := e.deployment()
		if d.Status.ObservedGeneration < d.Generation || d.Status.AvailableReplicas < 1 {
			return fmt.Errorf("operator Deployment not Available")
		}
		if e.f.Subscription == "" {
			return nil
		}
		csv := e.csv()
		phase, _, err := unstructured.NestedString(csv.Object, "status", "phase")
		if err != nil {
			return err
		}
		if phase != "Succeeded" {
			return fmt.Errorf("CSV not Succeeded: %s", phase)
		}
		owned := false
		for _, owner := range d.OwnerReferences {
			if owner.UID == csv.GetUID() {
				owned = true
			}
		}
		if !owned {
			return fmt.Errorf("Deployment is not owned by installed CSV")
		}
		return nil
	})
	crd := npObject("apiextensions.k8s.io/v1", "CustomResourceDefinition", "", "mustgathers.operator.openshift.io")
	Expect(e.c.Get(e.ctx, client.ObjectKeyFromObject(crd), crd)).To(Succeed())
	conditions, _, err := unstructured.NestedSlice(crd.Object, "status", "conditions")
	Expect(err).NotTo(HaveOccurred())
	established := false
	for _, v := range conditions {
		c := v.(map[string]interface{})
		if c["type"] == "Established" && c["status"] == "True" {
			established = true
		}
	}
	Expect(established).To(BeTrue())
}
func (e *npEnvironment) noProxy() {
	for _, c := range e.deployment().Spec.Template.Spec.Containers {
		for _, v := range c.Env {
			if v.Name == "HTTP_PROXY" || v.Name == "HTTPS_PROXY" {
				if v.Value != "" || v.ValueFrom != nil {
					ginkgo.Skip("this scenario requires a proxy-free operator installation")
				}
			}
		}
	}
}
func (e *npEnvironment) disruptive() {
	if !e.f.IsolatedOperator || os.Getenv("MG368_DISRUPTIVE") != "true" {
		ginkgo.Skip("dedicated installation and MG368_DISRUPTIVE=true required for operator mutation")
	}
}
func (e *npEnvironment) edit(obj *unstructured.Unstructured, path []string, value interface{}) {
	e.wait(func() error {
		current := obj.DeepCopy()
		if err := e.c.Get(e.ctx, client.ObjectKeyFromObject(current), current); err != nil {
			return err
		}
		if value == nil {
			unstructured.RemoveNestedField(current.Object, path...)
		} else if err := unstructured.SetNestedField(current.Object, value, path...); err != nil {
			return err
		}
		return e.c.Update(e.ctx, current)
	})
}
func (e *npEnvironment) saveField(obj *unstructured.Unstructured, path ...string) {
	key := obj.GetKind() + "/" + obj.GetNamespace() + "/" + obj.GetName() + ":" + strings.Join(path, ".")
	if e.savedFields[key] {
		return
	}
	e.savedFields[key] = true
	original, found, err := unstructured.NestedFieldCopy(obj.Object, path...)
	Expect(err).NotTo(HaveOccurred())
	if !found {
		original = nil
	}
	var priorProxy map[string]map[string]corev1.EnvVar
	if strings.Join(path, ".") == "spec.config.env" {
		priorProxy = proxyEnvironment(e.deployment())
	}
	e.restores = append(e.restores, func() {
		e.edit(obj, path, original)
		if priorProxy != nil {
			e.wait(func() error {
				d := e.deployment()
				if !reflect.DeepEqual(proxyEnvironment(d), priorProxy) || d.Status.ObservedGeneration < d.Generation || d.Status.UpdatedReplicas != ptr.Deref(d.Spec.Replicas, 1) || d.Status.ReadyReplicas != ptr.Deref(d.Spec.Replicas, 1) || d.Status.Replicas != ptr.Deref(d.Spec.Replicas, 1) {
					return fmt.Errorf("restored proxy env rollout pending")
				}
				return nil
			})
		}
	})
	ginkgo.DeferCleanup(e.restore)
}
func (e *npEnvironment) restore() {
	if len(e.restores) == 0 {
		return
	}
	restores := e.restores
	e.restores = nil
	e.savedFields = map[string]bool{}
	// Cleanup must still work after the per-case context times out.
	previous := e.ctx
	ctx, cancel := context.WithTimeout(context.Background(), CleanupTimeout)
	e.ctx = ctx
	defer func() { e.ctx = previous; cancel() }()
	for i := len(restores) - 1; i >= 0; i-- {
		restores[i]()
	}
	e.health()
}
func proxyEnvironment(d *appsv1.Deployment) map[string]map[string]corev1.EnvVar {
	result := map[string]map[string]corev1.EnvVar{}
	for _, c := range d.Spec.Template.Spec.Containers {
		values := map[string]corev1.EnvVar{}
		for _, v := range c.Env {
			if v.Name == "HTTP_PROXY" || v.Name == "HTTPS_PROXY" || v.Name == "NO_PROXY" {
				values[v.Name] = v
			}
		}
		result[c.Name] = values
	}
	return result
}
func (e *npEnvironment) setProxy(http, https, noProxy string) {
	e.disruptive()
	apiURL, err := url.Parse(e.cfg.Host)
	Expect(err).NotTo(HaveOccurred())
	noProxy = strings.Join([]string{noProxy, apiURL.Hostname(), "kubernetes.default.svc", ".svc", ".cluster.local", "localhost", "127.0.0.1"}, ",")
	sub := e.subscription()
	e.saveField(sub, "spec", "config", "env")
	env, _, err := unstructured.NestedSlice(sub.Object, "spec", "config", "env")
	Expect(err).NotTo(HaveOccurred())
	next := []interface{}{}
	for _, v := range env {
		item := v.(map[string]interface{})
		if item["name"] != "HTTP_PROXY" && item["name"] != "HTTPS_PROXY" && item["name"] != "NO_PROXY" {
			next = append(next, item)
		}
	}
	for _, pair := range [][2]string{{"HTTP_PROXY", http}, {"HTTPS_PROXY", https}, {"NO_PROXY", noProxy}} {
		next = append(next, map[string]interface{}{"name": pair[0], "value": pair[1]})
	}
	e.edit(sub, []string{"spec", "config", "env"}, next)
	e.wait(func() error {
		d := e.deployment()
		for _, c := range d.Spec.Template.Spec.Containers {
			values := map[string]string{}
			for _, v := range c.Env {
				values[v.Name] = v.Value
			}
			if values["HTTP_PROXY"] == http && values["HTTPS_PROXY"] == https && values["NO_PROXY"] == noProxy && d.Status.ObservedGeneration >= d.Generation && d.Status.UpdatedReplicas == ptr.Deref(d.Spec.Replicas, 1) && d.Status.ReadyReplicas == ptr.Deref(d.Spec.Replicas, 1) && d.Status.Replicas == ptr.Deref(d.Spec.Replicas, 1) {
				return nil
			}
		}
		return fmt.Errorf("operator env rollout pending")
	})
	e.health()
}
func proxyURL(scheme, host string, port int) string {
	return scheme + "://" + net.JoinHostPort(host, strconv.Itoa(port))
}
func (e *npEnvironment) proxyFixtures() {
	f := e.f.Proxies
	if f.HTTPHost == "" || f.HTTPSHost == "" || f.HTTPPort == 0 || f.HTTPSPort == 0 || f.LogPod == "" || f.LogNamespace == "" {
		ginkgo.Skip("controlled proxy endpoints and CONNECT log Pod are required")
	}
}
func (e *npEnvironment) proxyLog(since time.Time, host string) {
	f := e.f.Proxies
	e.wait(func() error {
		logs, err := npLogs(e.ctx, e.cs, f.LogNamespace, f.LogPod, f.LogContainer, since)
		if err != nil {
			return err
		}
		if !strings.Contains(logs, "CONNECT") || !strings.Contains(logs, host) {
			return fmt.Errorf("fixture proxy CONNECT evidence missing")
		}
		return nil
	})
}
func (e *npEnvironment) replicas(count int64) {
	e.disruptive()
	csv := e.csv()
	e.saveField(csv, "spec", "install", "spec", "deployments")
	deployments, _, err := unstructured.NestedSlice(csv.Object, "spec", "install", "spec", "deployments")
	Expect(err).NotTo(HaveOccurred())
	found := false
	for _, v := range deployments {
		d := v.(map[string]interface{})
		if d["name"] == e.f.Deployment {
			spec := d["spec"].(map[string]interface{})
			spec["replicas"] = count
			found = true
		}
	}
	Expect(found).To(BeTrue())
	e.edit(csv, []string{"spec", "install", "spec", "deployments"}, deployments)
	e.wait(func() error {
		d := e.deployment()
		pods := &corev1.PodList{}
		sel, err := metav1.LabelSelectorAsSelector(d.Spec.Selector)
		if err != nil {
			return err
		}
		if err := e.c.List(e.ctx, pods, client.InNamespace(e.f.OperatorNamespace), client.MatchingLabelsSelector{Selector: sel}); err != nil {
			return err
		}
		if int64(len(pods.Items)) != count {
			return fmt.Errorf("operator replica transition pending")
		}
		return nil
	})
}
func (e *npEnvironment) restartOperator() {
	d := e.deployment()
	selector, err := metav1.LabelSelectorAsSelector(d.Spec.Selector)
	Expect(err).NotTo(HaveOccurred())
	pods := &corev1.PodList{}
	Expect(e.c.List(e.ctx, pods, client.InNamespace(e.f.OperatorNamespace), client.MatchingLabelsSelector{Selector: selector})).To(Succeed())
	Expect(pods.Items).To(HaveLen(1))
	p := pods.Items[0]
	controller := metav1.GetControllerOf(&p)
	Expect(controller).NotTo(BeNil())
	Expect(controller.Kind).To(Equal("ReplicaSet"))
	rs := &appsv1.ReplicaSet{}
	Expect(e.c.Get(e.ctx, client.ObjectKey{Namespace: p.Namespace, Name: controller.Name}, rs)).To(Succeed())
	Expect(metav1.IsControlledBy(rs, d)).To(BeTrue())
	e.delete(&p)
	e.wait(func() error {
		list := &corev1.PodList{}
		if err := e.c.List(e.ctx, list, client.InNamespace(d.Namespace), client.MatchingLabelsSelector{Selector: selector}); err != nil {
			return err
		}
		for _, next := range list.Items {
			if next.UID != p.UID && next.Status.Phase == corev1.PodRunning {
				return nil
			}
		}
		return fmt.Errorf("replacement operator Pod missing")
	})
	e.health()
}

func (e *npEnvironment) checkPermissions() {
	if e.f.Subscription == "" {
		return
	}
	csv := e.csv()
	permissions, _, err := unstructured.NestedSlice(csv.Object, "spec", "install", "spec", "clusterPermissions")
	Expect(err).NotTo(HaveOccurred())
	sa := e.deployment().Spec.Template.Spec.ServiceAccountName
	declared := map[string]bool{}
	for _, v := range permissions {
		p := v.(map[string]interface{})
		if p["serviceAccountName"] != sa {
			continue
		}
		for _, v := range p["rules"].([]interface{}) {
			rule := v.(map[string]interface{})
			if strings.Contains(fmt.Sprint(rule["apiGroups"]), "networking.k8s.io") && strings.Contains(fmt.Sprint(rule["resources"]), "networkpolicies") {
				for _, verb := range rule["verbs"].([]interface{}) {
					declared[fmt.Sprint(verb)] = true
				}
			}
		}
	}
	for _, verb := range []string{"create", "delete", "get", "list", "update", "watch"} {
		Expect(declared[verb] || declared["*"]).To(BeTrue(), "installed CSV lacks NetworkPolicy %s", verb)
	}
}
func (e *npEnvironment) customImage() *mgv1.ImageStreamTagRef {
	if e.f.CustomImage == "" {
		ginkgo.Skip("customImage fixture is required")
	}
	stream := ImageStream(e.f.OperatorNamespace, e.ns, e.f.CustomImage)
	e.create(stream)
	e.wait(func() error {
		if err := e.c.Get(e.ctx, client.ObjectKeyFromObject(stream), stream); err != nil {
			return err
		}
		for _, tag := range stream.Status.Tags {
			if tag.Tag == "test" && len(tag.Items) > 0 && tag.Items[0].DockerImageReference != "" {
				return nil
			}
		}
		return fmt.Errorf("ImageStream fixture import pending")
	})
	return &mgv1.ImageStreamTagRef{Name: stream.Name, Tag: "test"}
}

func TestHoldScriptItemLengths(t *testing.T) {
	for _, real := range []bool{false, true} {
		for i, s := range HoldScript(real) {
			if len(s) > 256 {
				t.Fatalf("HoldScript(%v)[%d] length %d exceeds 256: %q", real, i, len(s), s)
			}
		}
	}
}

func TestProbePodCommandStartsListener(t *testing.T) {
	p := ProbePod("ns", "control", "img", "")
	cmd := p.Spec.Containers[0].Command
	if len(cmd) < 3 || cmd[0] != "python3" || cmd[1] != "-c" {
		t.Fatalf("unexpected command: %#v", cmd)
	}
	for _, part := range []string{"18080", "bind", "listen"} {
		if !strings.Contains(cmd[2], part) {
			t.Fatalf("probe command missing %q: %s", part, cmd[2])
		}
	}
}
