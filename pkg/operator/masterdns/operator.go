package masterdns

import (
	"context"
	"fmt"
	"os"
	"time"

	log "github.com/sirupsen/logrus"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	kubeinformers "k8s.io/client-go/informers"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	clusterclientset "sigs.k8s.io/cluster-api/pkg/client/clientset_generated/clientset"
	clusterinformers "sigs.k8s.io/cluster-api/pkg/client/informers_generated/externalversions"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	masterdnsv1 "github.com/openshift/master-dns-operator/pkg/apis/masterdns/v1alpha1"
	"github.com/openshift/master-dns-operator/pkg/operator/assets"
)

const (
	clusterAPINamespace      = "openshift-cluster-api"
	operatorNamespace        = "openshift-master-dns-operator"
	workloadFailingCondition = "WorkloadFailing"
	masterLabelSelector      = "sigs.k8s.io/cluster-api-machine-role=master"
	endpointsResourceName    = "masters"
	externalDNSNamespace     = "openshift-master-dns"
	clusterConfigNamespace   = "kube-system"
	clusterConfigName        = "cluster-config-v1"
	installConfigKey         = "install-config"
)

var (
	configName      = types.NamespacedName{Name: "instance", Namespace: ""}
	endpointsName   = types.NamespacedName{Name: endpointsResourceName, Namespace: externalDNSNamespace}
	deploymentName  = types.NamespacedName{Name: "external-dns", Namespace: externalDNSNamespace}
	bindingName     = types.NamespacedName{Name: "external-dns"}
	roleName        = types.NamespacedName{Name: "external-dns"}
	svcAccountName  = types.NamespacedName{Name: "external-dns", Namespace: externalDNSNamespace}
	requeueInterval = 10 * time.Second
)

// Add creates a new MasterDNS Operator and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	log.Info("Adding MasterDNS operator to manager")
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileMasterDNS{
		client: mgr.GetClient(),
		scheme: mgr.GetScheme(),
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Ensure that a config instance exists
	dynamicClient, err := dynamic.NewForConfig(mgr.GetConfig())
	if err != nil {
		return err
	}
	v1helpers.EnsureOperatorConfigExists(
		dynamicClient,
		assets.MustAsset("config/operator-config.yaml"),
		schema.GroupVersionResource{Group: masterdnsv1.SchemeGroupVersion.Group, Version: "v1alpha1", Resource: "masterdnsoperatorconfigs"},
	)
	mdnsReconciler := r.(*ReconcileMasterDNS)

	mdnsReconciler.imagePullSpec = os.Getenv("IMAGE")
	if len(mdnsReconciler.imagePullSpec) == 0 {
		return fmt.Errorf("no image specified, specify one via the IMAGE env var")
	}

	mdnsReconciler.kubeClient, err = kubeclient.NewForConfig(mgr.GetConfig())
	if err != nil {
		return err
	}
	mdnsReconciler.clusterAPIClient, err = clusterclientset.NewForConfig(mgr.GetConfig())
	if err != nil {
		return err
	}

	mdnsReconciler.eventRecorder = setupEventRecorder(mdnsReconciler.kubeClient)

	// Create a new controller
	c, err := controller.New("master-dns-operator", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource MasterDNSOperatorConfig
	err = c.Watch(&source.Kind{Type: &masterdnsv1.MasterDNSOperatorConfig{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	clusterAPIInformerFactory := clusterinformers.NewSharedInformerFactoryWithOptions(mdnsReconciler.clusterAPIClient, 10*time.Minute, clusterinformers.WithNamespace(clusterAPINamespace))
	machineInformer := clusterAPIInformerFactory.Cluster().V1alpha1().Machines().Informer()
	mgr.Add(manager.RunnableFunc(func(stopch <-chan struct{}) error {
		machineInformer.Run(stopch)
		cache.WaitForCacheSync(stopch, machineInformer.HasSynced)
		return nil
	}))

	configInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(mdnsReconciler.kubeClient, 10*time.Minute, kubeinformers.WithNamespace(clusterConfigNamespace))
	configInformer := configInformerFactory.Core().V1().ConfigMaps().Informer()
	mgr.Add(manager.RunnableFunc(func(stopch <-chan struct{}) error {
		configInformer.Run(stopch)
		cache.WaitForCacheSync(stopch, configInformer.HasSynced)
		return nil
	}))

	handlerFunc := handler.ToRequestsFunc(func(mapObject handler.MapObject) []reconcile.Request {
		return []reconcile.Request{{NamespacedName: configName}}
	})

	// Watch for changes to machines in cluster API namespace
	err = c.Watch(&source.Informer{Informer: machineInformer}, &handler.EnqueueRequestsFromMapFunc{ToRequests: handlerFunc})
	if err != nil {
		return err
	}

	// Watch for Deployments
	err = c.Watch(&source.Kind{Type: &appsv1.Deployment{}}, &handler.EnqueueRequestsFromMapFunc{ToRequests: handlerFunc})
	if err != nil {
		return err
	}

	// Watch for Service Accounts
	err = c.Watch(&source.Kind{Type: &corev1.ServiceAccount{}}, &handler.EnqueueRequestsFromMapFunc{ToRequests: handlerFunc})
	if err != nil {
		return err
	}

	// Watch for DNS endpoints
	err = c.Watch(&source.Kind{Type: &masterdnsv1.DNSEndpoint{}}, &handler.EnqueueRequestsFromMapFunc{ToRequests: handlerFunc})
	if err != nil {
		return err
	}

	// Watch for Install Config
	err = c.Watch(&source.Informer{Informer: configInformer},
		&handler.EnqueueRequestsFromMapFunc{
			ToRequests: handler.ToRequestsFunc(func(o handler.MapObject) []reconcile.Request {
				if o.Meta.GetName() == clusterConfigName && o.Meta.GetNamespace() == clusterConfigNamespace {
					return handlerFunc(o)
				}
				return []reconcile.Request{}
			}),
		},
	)
	if err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcileMasterDNS{}

// ReconcileMasterDNS reconciles a MasterDNSOperatorConfig object
type ReconcileMasterDNS struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client           client.Client
	scheme           *runtime.Scheme
	kubeClient       kubeclient.Interface
	clusterAPIClient clusterclientset.Interface
	eventRecorder    events.Recorder
	imagePullSpec    string
}

// Reconcile reads that state of the cluster for a MasterDNSOperatorConfig object and makes changes based on the state read
// and what is in the MasterDNSOperatorConfig.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
// +kubebuilder:rbac:groups=masterdns.operator.openshift.io,resources=masterdnsoperatorconfig;masterdnsoperatorconfig/status,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles;clusterrolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=serviceaccounts;configmaps,verbs=get;list;watch;create;update;patch;delete
func (r *ReconcileMasterDNS) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	log.Printf("Reconciling MasterDNS config")

	// Fetch the MasterDNSOperatorConfig instance
	operatorConfig := &masterdnsv1.MasterDNSOperatorConfig{}
	err := r.client.Get(context.TODO(), request.NamespacedName, operatorConfig)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Warning("Master DNS operator config not found. Probing until one is created.")
			// Config not found. Keep retrying until one exists.
			return reconcile.Result{Requeue: true, RequeueAfter: 2 * time.Minute}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	switch operatorConfig.Spec.ManagementState {
	case operatorv1.Unmanaged:
		return reconcile.Result{}, nil

	case operatorv1.Removed:
		err = r.removeExternalDNS()
		return reconcile.Result{}, err
	}

	installConfig, err := getInstallConfig(r.kubeClient)
	if err != nil {
		return reconcile.Result{}, err
	}

	if !isConfigSupported(installConfig) {
		log.Warning("Installed provider not supported. Nothing to do.")
		return reconcile.Result{}, nil
	}

	providerArgs := getProviderArgs(installConfig)

	// Ensure that External DNS is deployed
	err = r.syncExternalDNS(operatorConfig, providerArgs)
	if err != nil {
		log.WithError(err).Error("An error occurred synchronizing the external DNS deployment")
		return reconcile.Result{}, err
	}

	// Ensure a dnsendpoint resource for masters exists with entries for every master machine
	err = r.syncDNSEndpointResource(installConfig)
	if err != nil {
		log.WithError(err).Error("An error occurred synchronizing the masters endpoint instance")
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}
