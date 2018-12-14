package masterdns

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"sort"

	log "github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/pkg/apis/cluster/v1alpha1"

	installertypes "github.com/openshift/installer/pkg/types"

	masterdnsv1 "github.com/openshift/master-dns-operator/pkg/apis/masterdns/v1alpha1"
)

var (
	masterMachinePattern = regexp.MustCompile(`(.+)-master-([0-9]+)`)
)

func (r *ReconcileMasterDNS) syncDNSEndpointResource(installConfig *installertypes.InstallConfig) error {
	masterMachines, err := r.clusterAPIClient.ClusterV1alpha1().Machines(clusterAPINamespace).List(metav1.ListOptions{LabelSelector: masterLabelSelector})
	if err != nil {
		log.WithError(err).Infof("Error fetching master machines")
		return err
	}
	endpoints := []*masterdnsv1.Endpoint{}
	for _, machine := range masterMachines.Items {
		endpoint := &masterdnsv1.Endpoint{}
		if !masterMachinePattern.MatchString(machine.Name) {
			log.WithField("machine", machine.Name).Warning("Machine name does not match expected pattern")
			continue
		}
		address := machineInternalIP(&machine)
		if len(address) == 0 {
			log.WithField("machine", machine.Name).Warning("Skipping machine because it doesn't currently have an internal IP")
			continue
		}
		nameParts := masterMachinePattern.FindStringSubmatch(machine.Name)
		endpoint.DNSName = fmt.Sprintf("%s-etcd-%s.%s", nameParts[1], nameParts[2], installConfig.BaseDomain)
		endpoint.RecordType = "A"
		endpoint.RecordTTL = 60
		endpoint.Targets = masterdnsv1.Targets{address}

		endpoints = append(endpoints, endpoint)
	}
	sort.Sort(endpointsByName(endpoints))

	dnsEndpoint := &masterdnsv1.DNSEndpoint{}
	err = r.client.Get(context.TODO(), endpointsName, dnsEndpoint)
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	// If instance has not been created, create it
	if err != nil {
		dnsEndpoint.Name = endpointsResourceName
		dnsEndpoint.Namespace = externalDNSNamespace
		dnsEndpoint.Spec.Endpoints = endpoints
		return r.client.Create(context.TODO(), dnsEndpoint)
	}

	if !reflect.DeepEqual(dnsEndpoint.Spec.Endpoints, endpoints) {
		dnsEndpoint.Spec.Endpoints = endpoints
		return r.client.Update(context.TODO(), dnsEndpoint)
	}

	log.Info("DNSEndpoint instance is up to date")

	return nil
}

func machineInternalIP(machine *clusterv1.Machine) string {
	for _, address := range machine.Status.Addresses {
		if address.Type == corev1.NodeInternalIP {
			return address.Address
		}
	}
	return ""
}

type endpointsByName []*masterdnsv1.Endpoint

func (s endpointsByName) Len() int           { return len(s) }
func (s endpointsByName) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s endpointsByName) Less(i, j int) bool { return s[i].DNSName < s[j].DNSName }
