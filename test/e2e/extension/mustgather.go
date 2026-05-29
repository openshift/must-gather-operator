package oap

import (
	"github.com/openshift/must-gather-operator/test/e2e/extension/testdata"
	"path/filepath"
	"strings"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	compat_otp "github.com/openshift/origin/test/extended/util/compat_otp"
	clusterinfra "github.com/openshift/origin/test/extended/util/compat_otp/clusterinfra"
	e2e "k8s.io/kubernetes/test/e2e/framework"
)

var _ = g.Describe("[OTP][sig-oap] OAP must-gather", func() {
	defer g.GinkgoRecover()

	var (
		oc                  = compat_otp.NewCLI("must-gather", compat_otp.KubeConfigPath())
		buildPruningBaseDir = testdata.FixturePath("mustgather")
		cfg                 = olmInstallConfig{
			mode:                "OLMv0",
			operatorNamespace:   MGONamespace,
			buildPruningBaseDir: buildPruningBaseDir,
			subscriptionName:    MGOSubscriptionName,
			channel:             MGOChannelName,
			packageName:         "support-log-gather-operator",
			extensionName:       MGOExtensionName,
			serviceAccountName:  "sa-must-gather",
		}
	)
	g.BeforeEach(func() {
		if !IsDeploymentReady(oc, MGONamespace, MGODeploymentName) {
			e2e.Logf("Creating Must Gather Operator...")
			installMustGatherOperator(oc, cfg)
		}

	})

	// author: jitli@redhat.com
	g.It("Author:jitli-ROSA-ConnectedOnly-Critical-83956-Install MustGather Operator and verify pod readiness", func() {

		mgName := "mustgather-83956-" + getRandomString(4)
		ftpSecretName := "mustgather-creds" + getRandomString(4)

		compat_otp.By("Create secret that contains SFTP credentials")
		defer func() {
			err := cleanupSecret(oc, MGONamespace, ftpSecretName)
			o.Expect(err).NotTo(o.HaveOccurred())
		}()
		err := createSFTPCredentialSecret(oc, MGONamespace, ftpSecretName, DefaultSFTPUsername, DefaultSFTPPassword)
		o.Expect(err).NotTo(o.HaveOccurred())

		compat_otp.By("Create mustgather")
		defer func() {
			e2e.Logf("Cleanup the mustgather")
			err := oc.AsAdmin().Run("delete").Args("-n", MGONamespace, "mustgather", mgName, "--ignore-not-found").Execute()
			o.Expect(err).NotTo(o.HaveOccurred())
		}()
		mustGatherTemplate := filepath.Join(buildPruningBaseDir, "mustgather.yaml")
		params := []string{"-f", mustGatherTemplate, "-p", "NAME=" + mgName, "NAMESPACE=" + MGONamespace, "CASESECRET=" + ftpSecretName}
		compat_otp.ApplyNsResourceFromTemplate(oc, MGONamespace, params...)

		compat_otp.By("Verify must-gather pod logs for successful completion")
		err = verifyMustGatherLogs(oc, MGONamespace, mgName)
		if err != nil {
			dumpResource(oc, MGONamespace, "mustgather", mgName, "-o=yaml")
		}
		o.Expect(err).NotTo(o.HaveOccurred())

	})

	// author: jitli@redhat.com
	g.It("Author:jitli-ROSA-High-83942-Support uploading must-gather bundles through proxy-enabled environments", func() {

		// applicable variants: proxy-enabled clusters only
		compat_otp.By("Check if cluster is proxy-enabled")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("proxy", "cluster", "-o", "jsonpath={.spec}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		if !strings.Contains(output, "httpsProxy") && !strings.Contains(output, "httpProxy") {
			g.Skip("Skip for non-proxy cluster - this test requires a proxy-enabled environment")
		}

		compat_otp.By("Get proxy configuration from cluster")
		httpProxy, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("proxy", "cluster", "-o=jsonpath={.spec.httpProxy}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		httpsProxy, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("proxy", "cluster", "-o=jsonpath={.spec.httpsProxy}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		noProxy, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("proxy", "cluster", "-o=jsonpath={.spec.noProxy}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())

		e2e.Logf("Cluster proxy configuration:")
		e2e.Logf("  HTTP_PROXY: %s", httpProxy)
		e2e.Logf("  HTTPS_PROXY: %s", httpsProxy)
		e2e.Logf("  NO_PROXY: %s", noProxy)

		o.Expect(httpProxy).NotTo(o.BeEmpty(), "httpProxy should not be empty in proxy-enabled cluster")

		compat_otp.By("Verify must-gather operator pod has proxy environment variables matching cluster config")
		operatorPod, err := oc.AsAdmin().Run("get").Args("pods", "-n", MGONamespace, "-l", "name=must-gather-operator", "-o=jsonpath={.items[0].metadata.name}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(operatorPod).NotTo(o.BeEmpty(), "operator pod should exist")
		e2e.Logf("Found operator pod: %s", operatorPod)

		operatorHTTPProxy, err := oc.AsAdmin().Run("get").Args("pod", operatorPod, "-n", MGONamespace, "-o=jsonpath={.spec.containers[0].env[?(@.name=='HTTP_PROXY')].value}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		operatorHTTPSProxy, err := oc.AsAdmin().Run("get").Args("pod", operatorPod, "-n", MGONamespace, "-o=jsonpath={.spec.containers[0].env[?(@.name=='HTTPS_PROXY')].value}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		operatorNoProxy, err := oc.AsAdmin().Run("get").Args("pod", operatorPod, "-n", MGONamespace, "-o=jsonpath={.spec.containers[0].env[?(@.name=='NO_PROXY')].value}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())

		e2e.Logf("Operator pod proxy configuration:")
		e2e.Logf("  HTTP_PROXY: %s", operatorHTTPProxy)
		e2e.Logf("  HTTPS_PROXY: %s", operatorHTTPSProxy)
		e2e.Logf("  NO_PROXY: %s", operatorNoProxy)

		o.Expect(operatorHTTPProxy).To(o.Equal(httpProxy), "operator pod HTTP_PROXY should match cluster proxy config")
		o.Expect(operatorHTTPSProxy).To(o.Equal(httpsProxy), "operator pod HTTPS_PROXY should match cluster proxy config")
		o.Expect(operatorNoProxy).To(o.ContainSubstring(noProxy), "operator pod NO_PROXY should contain cluster NO_PROXY config")
		e2e.Logf("Operator pod has correct proxy environment variables matching cluster config")

	})

	// author: jitli@redhat.com
	g.It("Author:jitli-ROSA-High-85844-mustgather stop work within the specified mustGatherTimeout time", func() {

		mgName := "mustgather-85844-" + getRandomString(4)
		mgName2 := "mustgather-85844-" + getRandomString(4)
		mgName3 := "mustgather-85844-" + getRandomString(4)

		compat_otp.By("Create mustgather with mustGatherTimeout set to 10s")
		defer func() {
			e2e.Logf("Cleanup the mustgather")
			err := oc.AsAdmin().Run("delete").Args("-n", MGONamespace, "mustgather", mgName, "--ignore-not-found").Execute()
			o.Expect(err).NotTo(o.HaveOccurred())
		}()
		mustGatherTimeoutTemplate := filepath.Join(buildPruningBaseDir, "mustgather-timeout.yaml")
		params := []string{
			"-f", mustGatherTimeoutTemplate,
			"-p", "NAME=" + mgName,
			"NAMESPACE=" + MGONamespace,
			"TIMEOUT=" + "10s",
		}
		compat_otp.ApplyNsResourceFromTemplate(oc, MGONamespace, params...)

		compat_otp.By("Verify mustGatherTimeout field is set correctly")
		err := verifyMustGatherTimeout(oc, MGONamespace, mgName, "10s")
		o.Expect(err).NotTo(o.HaveOccurred())

		compat_otp.By("Verify MustGather CR status becomes Completed after timeout")
		err = waitForMustGatherCompletion(oc, MGONamespace, mgName, 20*time.Second)
		if err != nil {
			dumpResource(oc, MGONamespace, "mustgather", mgName, "-o=yaml")
		}
		o.Expect(err).NotTo(o.HaveOccurred())

		compat_otp.By("Verify pod is destroyed after timeout")
		err = verifyPodDestroyed(oc, MGONamespace, mgName, 20*time.Second)
		if err != nil {
			dumpResource(oc, MGONamespace, "mustgather", mgName, "-o=yaml")
		}
		o.Expect(err).NotTo(o.HaveOccurred())

		compat_otp.By("Create mustgather with mustGatherTimeout set to 10m")
		defer func() {
			e2e.Logf("Cleanup the mustgather")
			err := oc.AsAdmin().Run("delete").Args("-n", MGONamespace, "mustgather", mgName2, "--ignore-not-found").Execute()
			o.Expect(err).NotTo(o.HaveOccurred())
		}()
		params2 := []string{
			"-f", mustGatherTimeoutTemplate,
			"-p", "NAME=" + mgName2,
			"NAMESPACE=" + MGONamespace,
			"TIMEOUT=" + "10m",
		}
		compat_otp.ApplyNsResourceFromTemplate(oc, MGONamespace, params2...)

		compat_otp.By("Verify mustGatherTimeout field is set correctly")
		err = verifyMustGatherTimeout(oc, MGONamespace, mgName2, "10m0s")
		o.Expect(err).NotTo(o.HaveOccurred())

		compat_otp.By("Create mustgather with mustGatherTimeout set to 10h")
		defer func() {
			e2e.Logf("Cleanup the mustgather")
			err := oc.AsAdmin().Run("delete").Args("-n", MGONamespace, "mustgather", mgName3, "--ignore-not-found").Execute()
			o.Expect(err).NotTo(o.HaveOccurred())
		}()
		params3 := []string{
			"-f", mustGatherTimeoutTemplate,
			"-p", "NAME=" + mgName3,
			"NAMESPACE=" + MGONamespace,
			"TIMEOUT=" + "10h",
		}
		compat_otp.ApplyNsResourceFromTemplate(oc, MGONamespace, params3...)

		compat_otp.By("Verify mustGatherTimeout field is set correctly")
		err = verifyMustGatherTimeout(oc, MGONamespace, mgName3, "10h0m0s")
		o.Expect(err).NotTo(o.HaveOccurred())

		e2e.Logf("Successfully verified mustGatherTimeout functionality")
	})

	// author: jitli@redhat.com
	g.It("Author:jitli-ROSA-Critical-84264-Flag based deletion of must-gather job and CR [Slow]", func() {

		//There are currently no real accounts available for CI testing; local testing is the only option.
		skipIfNotLocal(Local)

		compat_otp.By("Default behavior - retainResourcesOnCompletion should default to false")
		mgName1 := "mustgather-84264-" + getRandomString(4)
		ftpSecretName := "mustgather-creds-" + getRandomString(4)

		compat_otp.By("Create secret that contains SFTP credentials")
		defer func() {
			err := cleanupSecret(oc, MGONamespace, ftpSecretName)
			o.Expect(err).NotTo(o.HaveOccurred())
		}()
		err := createSFTPCredentialSecret(oc, MGONamespace, ftpSecretName, DefaultSFTPUsername, DefaultSFTPPassword)
		o.Expect(err).NotTo(o.HaveOccurred())

		compat_otp.By("Create mustgather without setting retainResourcesOnCompletion field")
		mustGatherTemplate := filepath.Join(buildPruningBaseDir, "mustgather.yaml")
		params := []string{"-f", mustGatherTemplate, "-p", "NAME=" + mgName1, "NAMESPACE=" + MGONamespace, "CASESECRET=" + ftpSecretName}
		compat_otp.ApplyNsResourceFromTemplate(oc, MGONamespace, params...)

		compat_otp.By("Verify retainResourcesOnCompletion defaults to false")
		err = verifyRetainResourcesField(oc, MGONamespace, mgName1, false)
		o.Expect(err).NotTo(o.HaveOccurred())

		compat_otp.By("Cleanup MustGather CR")
		err = oc.AsAdmin().Run("delete").Args("-n", MGONamespace, "mustgather", mgName1, "--ignore-not-found").Execute()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Default behavior verified successfully")

		compat_otp.By("Test retainResourcesOnCompletion set to true")
		mgName2 := "mustgather-84264-retain-" + getRandomString(4)

		compat_otp.By("Create mustgather with retainResourcesOnCompletion set to true")
		defer func() {
			e2e.Logf("Cleanup the mustgather")
			err := oc.AsAdmin().Run("delete").Args("-n", MGONamespace, "mustgather", mgName2, "--ignore-not-found").Execute()
			o.Expect(err).NotTo(o.HaveOccurred())
		}()
		mustGatherRetainTemplate := filepath.Join(buildPruningBaseDir, "mustgather-retain.yaml")
		params2 := []string{
			"-f", mustGatherRetainTemplate,
			"-p", "NAME=" + mgName2,
			"NAMESPACE=" + MGONamespace,
			"CASESECRET=" + ftpSecretName,
			"RETAIN_RESOURCES=true",
		}
		compat_otp.ApplyNsResourceFromTemplate(oc, MGONamespace, params2...)

		compat_otp.By("Verify retainResourcesOnCompletion is set to true")
		err = verifyRetainResourcesField(oc, MGONamespace, mgName2, true)
		o.Expect(err).NotTo(o.HaveOccurred())

		compat_otp.By("Verify must-gather pod logs for successful completion")
		err = verifyMustGatherLogs(oc, MGONamespace, mgName2)
		if err != nil {
			dumpResource(oc, MGONamespace, "mustgather", mgName2, "-o=yaml")
		}
		o.Expect(err).NotTo(o.HaveOccurred())

		compat_otp.By("Wait for must-gather to complete")
		err = waitForMustGatherCompletion(oc, MGONamespace, mgName2, 10*time.Minute)
		if err != nil {
			dumpResource(oc, MGONamespace, "mustgather", mgName2, "-o=yaml")
		}
		o.Expect(err).NotTo(o.HaveOccurred())

		compat_otp.By("Verify pod is retained when retainResourcesOnCompletion is true")
		err = verifyPodRetained(oc, MGONamespace, mgName2)
		o.Expect(err).NotTo(o.HaveOccurred())

		e2e.Logf("Test retainResourcesOnCompletion=true verified successfully")
	})

	// author: jitli@redhat.com
	g.It("Author:jitli-ROSA-Critical-84371-Support enable or disable of the must-gather bundle upload", func() {

		mgName := "mustgather-84371-" + getRandomString(4)

		compat_otp.By("Create mustgather without uploadTarget configuration")
		defer func() {
			e2e.Logf("Cleanup the mustgather")
			err := oc.AsAdmin().Run("delete").Args("-n", MGONamespace, "mustgather", mgName, "--ignore-not-found").Execute()
			o.Expect(err).NotTo(o.HaveOccurred())
		}()
		mustGatherNoUploadTemplate := filepath.Join(buildPruningBaseDir, "mustgather-noupload.yaml")
		params := []string{
			"-f", mustGatherNoUploadTemplate,
			"-p", "NAME=" + mgName,
			"NAMESPACE=" + MGONamespace,
			"RETAIN_RESOURCES=true",
		}
		compat_otp.ApplyNsResourceFromTemplate(oc, MGONamespace, params...)

		compat_otp.By("Verify that must-gather pod does not have upload container")
		err := verifyNoUploadContainer(oc, MGONamespace, mgName)
		if err != nil {
			dumpResource(oc, MGONamespace, "mustgather", mgName, "-o=yaml")
		}
		o.Expect(err).NotTo(o.HaveOccurred())

		compat_otp.By("Wait for must-gather to complete")
		err = waitForMustGatherCompletion(oc, MGONamespace, mgName, 10*time.Minute)
		if err != nil {
			dumpResource(oc, MGONamespace, "mustgather", mgName, "-o=yaml")
		}
		o.Expect(err).NotTo(o.HaveOccurred())

		e2e.Logf("Successfully verified must-gather without upload functionality")
	})

	// author: jitli@redhat.com
	g.It("Author:jitli-ROSA-Critical-85767-84382-Support for PVC API in mustgather spec and verify must-gather.log [Slow]", func() {

		// Check StorageClass
		compat_otp.By("Check if cluster has StorageClass available")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("storageclass", "-o=jsonpath={.items[*].metadata.name}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		if output == "" {
			g.Skip("Skip: No StorageClass available in cluster - cannot create PVC")
		}

		// Skip for BareMetal platform
		clusterinfra.SkipTestIfNotSupportedPlatform(oc.AsAdmin(), clusterinfra.BareMetal)

		mgName := "mustgather-85767-" + getRandomString(4)
		pvcName := "mustgather-pvc-" + getRandomString(4)
		ftpSecretName := "mustgather-creds-" + getRandomString(4)
		readerPodName := "data-reader-pod-" + getRandomString(4)
		subPath := "must-gather-bundles/case-04230315-" + getRandomString(4)

		compat_otp.By("Create PersistentVolumeClaim")
		defer func() {
			err := cleanupPVC(oc, MGONamespace, pvcName)
			o.Expect(err).NotTo(o.HaveOccurred())
		}()
		pvcTemplate := filepath.Join(buildPruningBaseDir, "pvc.yaml")
		pvcParams := []string{
			"-f", pvcTemplate,
			"-p", "NAME=" + pvcName,
			"NAMESPACE=" + MGONamespace,
			"STORAGE=5Gi",
		}
		compat_otp.ApplyNsResourceFromTemplate(oc, MGONamespace, pvcParams...)

		compat_otp.By("Create secret that contains SFTP credentials")
		defer func() {
			err := cleanupSecret(oc, MGONamespace, ftpSecretName)
			o.Expect(err).NotTo(o.HaveOccurred())
		}()
		err = createSFTPCredentialSecret(oc, MGONamespace, ftpSecretName, DefaultSFTPUsername, DefaultSFTPPassword)
		o.Expect(err).NotTo(o.HaveOccurred())

		compat_otp.By("Create mustgather with PVC storage configuration")
		defer func() {
			e2e.Logf("Cleanup the mustgather")
			err := oc.AsAdmin().Run("delete").Args("-n", MGONamespace, "mustgather", mgName, "--ignore-not-found").Execute()
			o.Expect(err).NotTo(o.HaveOccurred())
		}()
		mustGatherPVCTemplate := filepath.Join(buildPruningBaseDir, "mustgather-pvc.yaml")
		params := []string{
			"-f", mustGatherPVCTemplate,
			"-p", "NAME=" + mgName,
			"NAMESPACE=" + MGONamespace,
			"CASESECRET=" + ftpSecretName,
			"PVCNAME=" + pvcName,
			"SUBPATH=" + subPath,
		}
		compat_otp.ApplyNsResourceFromTemplate(oc, MGONamespace, params...)

		compat_otp.By("Verify PVC becomes Bound")
		err = verifyPVCBound(oc, MGONamespace, pvcName, 2*time.Minute)
		if err != nil {
			dumpResource(oc, MGONamespace, "pvc", pvcName, "-o=yaml")
		}
		o.Expect(err).NotTo(o.HaveOccurred())

		compat_otp.By("Verify must-gather pod logs for successful completion")
		err = verifyMustGatherLogs(oc, MGONamespace, mgName)
		if err != nil {
			dumpResource(oc, MGONamespace, "mustgather", mgName, "-o=yaml")
		}
		o.Expect(err).NotTo(o.HaveOccurred())

		compat_otp.By("Create reader pod to access PVC data")
		defer func() {
			err := cleanupPod(oc, MGONamespace, readerPodName)
			o.Expect(err).NotTo(o.HaveOccurred())
		}()
		readerPodTemplate := filepath.Join(buildPruningBaseDir, "reader-pod.yaml")
		readerParams := []string{
			"-f", readerPodTemplate,
			"-p", "NAME=" + readerPodName,
			"NAMESPACE=" + MGONamespace,
			"PVCNAME=" + pvcName,
		}
		compat_otp.ApplyNsResourceFromTemplate(oc, MGONamespace, readerParams...)

		compat_otp.By("Wait for reader pod to be running")
		err = waitForPodRunning(oc, MGONamespace, readerPodName, 3*time.Minute)
		if err != nil {
			dumpResource(oc, MGONamespace, "pod", readerPodName, "-o=yaml")
		}
		o.Expect(err).NotTo(o.HaveOccurred())

		compat_otp.By("Verify PVC contains must-gather data")
		err = verifyPVCContainsData(oc, MGONamespace, readerPodName, subPath)
		o.Expect(err).NotTo(o.HaveOccurred())

		//Author:jitli-ROSA-Critical-84382-The uploaded tar bundle should include the log of gather process itself
		compat_otp.By("Verify must-gather.log content includes expected gather process messages")
		err = verifyMustGatherLogContent(oc, MGONamespace, readerPodName, subPath)
		o.Expect(err).NotTo(o.HaveOccurred())

		e2e.Logf("Successfully verified PVC storage functionality for must-gather")
	})

	// author: jitli@redhat.com
	g.It("Author:jitli-ROSA-High-86065-Verify audit field control over the collection of audit logs", func() {

		// Check StorageClass
		compat_otp.By("Check if cluster has StorageClass available")
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("storageclass", "-o=jsonpath={.items[*].metadata.name}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		if output == "" {
			g.Skip("Skip: No StorageClass available in cluster - cannot create PVC")
		}

		// Skip for BareMetal platform
		clusterinfra.SkipTestIfNotSupportedPlatform(oc.AsAdmin(), clusterinfra.BareMetal)

		mgNameWithAudit := "mustgather-86065-audit-" + getRandomString(4)
		mgNameNoAudit := "mustgather-86065-noaudit-" + getRandomString(4)
		pvcName := "mustgather-pvc-" + getRandomString(4)
		readerPodName := "data-reader-pod-" + getRandomString(4)
		subPathWithAudit := "must-gather-bundles/case-audit-" + getRandomString(4)

		compat_otp.By("Create PersistentVolumeClaim")
		defer func() {
			err := cleanupPVC(oc, MGONamespace, pvcName)
			o.Expect(err).NotTo(o.HaveOccurred())
		}()
		pvcTemplate := filepath.Join(buildPruningBaseDir, "pvc.yaml")
		pvcParams := []string{
			"-f", pvcTemplate,
			"-p", "NAME=" + pvcName,
			"NAMESPACE=" + MGONamespace,
			"STORAGE=5Gi",
		}
		compat_otp.ApplyNsResourceFromTemplate(oc, MGONamespace, pvcParams...)

		compat_otp.By("Create mustgather with audit: true")
		defer func() {
			e2e.Logf("Cleanup the mustgather with audit")
			err := oc.AsAdmin().Run("delete").Args("-n", MGONamespace, "mustgather", mgNameWithAudit, "--ignore-not-found").Execute()
			o.Expect(err).NotTo(o.HaveOccurred())
		}()
		mustGatherAuditTemplate := filepath.Join(buildPruningBaseDir, "mustgather-audit.yaml")
		params := []string{
			"-f", mustGatherAuditTemplate,
			"-p", "NAME=" + mgNameWithAudit,
			"NAMESPACE=" + MGONamespace,
			"PVCNAME=" + pvcName,
			"SUBPATH=" + subPathWithAudit,
			"AUDIT=true",
			"RETAIN_RESOURCES=true",
		}
		compat_otp.ApplyNsResourceFromTemplate(oc, MGONamespace, params...)

		compat_otp.By("Verify PVC becomes Bound")
		err = verifyPVCBound(oc, MGONamespace, pvcName, 2*time.Minute)
		if err != nil {
			dumpResource(oc, MGONamespace, "pvc", pvcName, "-o=yaml")
		}
		o.Expect(err).NotTo(o.HaveOccurred())

		compat_otp.By("Verify must-gather pod logs contain audit log collection messages")
		err = verifyAuditLogCollection(oc, MGONamespace, mgNameWithAudit)
		if err != nil {
			dumpResource(oc, MGONamespace, "mustgather", mgNameWithAudit, "-o=yaml")
		}
		o.Expect(err).NotTo(o.HaveOccurred())

		compat_otp.By("Wait for must-gather with audit to complete")
		err = waitForMustGatherCompletion(oc, MGONamespace, mgNameWithAudit, 10*time.Minute)
		if err != nil {
			dumpResource(oc, MGONamespace, "mustgather", mgNameWithAudit, "-o=yaml")
		}
		o.Expect(err).NotTo(o.HaveOccurred())

		compat_otp.By("Create reader pod to access PVC data")
		defer func() {
			err := cleanupPod(oc, MGONamespace, readerPodName)
			o.Expect(err).NotTo(o.HaveOccurred())
		}()
		readerPodTemplate := filepath.Join(buildPruningBaseDir, "reader-pod.yaml")
		readerParams := []string{
			"-f", readerPodTemplate,
			"-p", "NAME=" + readerPodName,
			"NAMESPACE=" + MGONamespace,
			"PVCNAME=" + pvcName,
		}
		compat_otp.ApplyNsResourceFromTemplate(oc, MGONamespace, readerParams...)

		compat_otp.By("Wait for reader pod to be running")
		err = waitForPodRunning(oc, MGONamespace, readerPodName, 3*time.Minute)
		if err != nil {
			dumpResource(oc, MGONamespace, "pod", readerPodName, "-o=yaml")
		}
		o.Expect(err).NotTo(o.HaveOccurred())

		compat_otp.By("Verify PVC contains audit_logs directory when audit: true")
		err = verifyAuditLogsDirectory(oc, MGONamespace, readerPodName, subPathWithAudit)
		o.Expect(err).NotTo(o.HaveOccurred())

		compat_otp.By("Verify default audit field is false")
		defer func() {
			e2e.Logf("Cleanup the mustgather without audit")
			err = oc.AsAdmin().Run("delete").Args("-n", MGONamespace, "mustgather", mgNameNoAudit, "--ignore-not-found").Execute()
			o.Expect(err).NotTo(o.HaveOccurred())
		}()
		mustGatherNoAuditTemplate := filepath.Join(buildPruningBaseDir, "mustgather-noupload.yaml")
		params2 := []string{
			"-f", mustGatherNoAuditTemplate,
			"-p", "NAME=" + mgNameNoAudit,
			"NAMESPACE=" + MGONamespace,
		}
		compat_otp.ApplyNsResourceFromTemplate(oc, MGONamespace, params2...)

		compat_otp.By("Verify spec.audit field defaults to false")
		auditField, err := oc.AsAdmin().Run("get").Args("-n", MGONamespace, "mustgather", mgNameNoAudit, "-o=jsonpath={.spec.audit}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		if auditField != "false" {
			e2e.Failf("spec.audit field is not false (got: %s), expected default value to be false", auditField)
		}
		e2e.Logf("Verified: spec.audit field defaults to false (output: '%s')", auditField)

		e2e.Logf("Successfully verified audit field control over audit logs collection")
	})

})
