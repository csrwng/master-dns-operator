package masterdns

import (
	"context"
	"fmt"

	"github.com/ghodss/yaml"
	log "github.com/sirupsen/logrus"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	kubeclient "k8s.io/client-go/kubernetes"

	operatorv1 "github.com/openshift/api/operator/v1"
	installertypes "github.com/openshift/installer/pkg/types"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	masterdnsv1 "github.com/openshift/master-dns-operator/pkg/apis/masterdns/v1alpha1"
	"github.com/openshift/master-dns-operator/pkg/operator/assets"
)

func (r *ReconcileMasterDNS) syncExternalDNS(config *masterdnsv1.MasterDNSOperatorConfig, providerArgs []string) error {

	errors := []error{}
	originalConfig := config.DeepCopy()

	results := resourceapply.ApplyDirectly(r.kubeClient, r.eventRecorder, assets.Asset,
		"config/role.yaml",
		"config/binding.yaml",
		"config/sa.yaml",
	)
	resourcesThatForceRedeployment := sets.NewString("config/sa.yaml")
	forceDeployment := config.Generation != config.Status.ObservedGeneration

	for _, currResult := range results {
		if currResult.Error != nil {
			log.WithError(currResult.Error).WithField("file", currResult.File).Error("Apply error")
			errors = append(errors, fmt.Errorf("%q (%T): %v", currResult.File, currResult.Type, currResult.Error))
			continue
		}

		if currResult.Changed && resourcesThatForceRedeployment.Has(currResult.File) {
			forceDeployment = true
		}
	}
	actualDeployment, _, err := r.syncExternalDNSDeployment(config, providerArgs, forceDeployment)
	if err != nil {
		log.WithError(err).WithField("file", "config/deployment.yaml").Error("Apply error")
		errors = append(errors, fmt.Errorf("%q: %v", "config/deployment.yaml", err))
	}

	if actualDeployment.Status.ReadyReplicas > 0 {
		v1helpers.SetOperatorCondition(&config.Status.Conditions, operatorv1.OperatorCondition{
			Type:               operatorv1.OperatorStatusTypeAvailable,
			Status:             operatorv1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
		})
	} else {
		v1helpers.SetOperatorCondition(&config.Status.Conditions, operatorv1.OperatorCondition{
			Type:               operatorv1.OperatorStatusTypeAvailable,
			Status:             operatorv1.ConditionFalse,
			Reason:             "NoPodsAvailable",
			Message:            "no deployment pods available on any node.",
			LastTransitionTime: metav1.Now(),
		})
	}

	config.Status.ObservedGeneration = config.ObjectMeta.Generation
	config.Status.ReadyReplicas = actualDeployment.Status.ReadyReplicas
	resourcemerge.SetDeploymentGeneration(&config.Status.Generations, actualDeployment)
	if len(errors) > 0 {
		message := ""
		for _, err := range errors {
			message = message + err.Error() + "\n"
		}
		v1helpers.SetOperatorCondition(&config.Status.Conditions, operatorv1.OperatorCondition{
			Type:               workloadFailingCondition,
			Status:             operatorv1.ConditionTrue,
			Message:            message,
			Reason:             "SyncError",
			LastTransitionTime: metav1.Now(),
		})
	} else {
		v1helpers.SetOperatorCondition(&config.Status.Conditions, operatorv1.OperatorCondition{
			Type:               workloadFailingCondition,
			Status:             operatorv1.ConditionFalse,
			LastTransitionTime: metav1.Now(),
		})
	}
	if !equality.Semantic.DeepEqual(config.Status, originalConfig.Status) {
		if err := r.client.Status().Update(context.TODO(), config); err != nil {
			log.WithError(err).Error("Status update error")
			return err
		}
	}

	if len(errors) > 0 {
		log.WithField("Errors", errors).Error("errors occurred, requeuing")
		return utilerrors.NewAggregate(errors)
	}
	return nil
}

func (r *ReconcileMasterDNS) syncExternalDNSDeployment(config *masterdnsv1.MasterDNSOperatorConfig, providerArgs []string, forceDeployment bool) (*appsv1.Deployment, bool, error) {
	deployment := resourceread.ReadDeploymentV1OrDie(assets.MustAsset("config/deployment.yaml"))
	container := &deployment.Spec.Template.Spec.Containers[0]
	container.Image = r.imagePullSpec
	container.ImagePullPolicy = corev1.PullAlways
	for _, arg := range providerArgs {
		container.Args = append(container.Args, arg)
	}
	if len(config.Spec.LogLevel) > 0 {
		container.Args = append(container.Args, fmt.Sprintf("--log-level=%s", config.Spec.LogLevel))
	}
	return resourceapply.ApplyDeployment(
		r.kubeClient.AppsV1(),
		r.eventRecorder,
		deployment,
		resourcemerge.ExpectedDeploymentGeneration(deployment, config.Status.Generations),
		forceDeployment)
}

func (r *ReconcileMasterDNS) removeExternalDNS() error {
	resources := []struct {
		name   types.NamespacedName
		object runtime.Object
	}{
		{name: deploymentName, object: &appsv1.Deployment{}},
		{name: bindingName, object: &rbacv1.ClusterRoleBinding{}},
		{name: roleName, object: &rbacv1.ClusterRole{}},
		{name: svcAccountName, object: &corev1.ServiceAccount{}},
	}

	for _, resource := range resources {
		err := r.client.Get(context.TODO(), resource.name, resource.object)
		if err == nil {
			err = r.client.Delete(context.TODO(), resource.object)
			if err != nil {
				return err
			}
		} else {
			if !errors.IsNotFound(err) {
				return err
			}
		}
	}
	return nil
}

func getInstallConfig(client kubeclient.Interface) (*installertypes.InstallConfig, error) {
	cm, err := client.CoreV1().ConfigMaps(clusterConfigNamespace).Get(clusterConfigName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed getting clusterconfig %s/%s: %v", clusterConfigNamespace, clusterConfigName, err)
	}
	installConfigData, ok := cm.Data[installConfigKey]
	if !ok {
		return nil, fmt.Errorf("missing %q in configmap", installConfigKey)
	}
	installConfig := &installertypes.InstallConfig{}
	if err := yaml.Unmarshal([]byte(installConfigData), installConfig); err != nil {
		return nil, fmt.Errorf("invalid InstallConfig: %v yaml: %s", err, installConfigData)
	}
	return installConfig, nil
}

func isConfigSupported(config *installertypes.InstallConfig) bool {
	// Currently only AWS is supported
	return config.Platform.AWS != nil // || config.Platform.OtherPlatform != nil ..., etc
}

func getProviderArgs(config *installertypes.InstallConfig) []string {
	switch {
	case config.Platform.AWS != nil:
		return []string{
			"--provider=aws",
			fmt.Sprintf("--domain-filter=%s", config.BaseDomain),
			"--aws-zone-type=private",
			fmt.Sprintf("--aws-zone-tags=openshiftClusterID=%s", config.ClusterID),
		}
	}
	return nil
}
