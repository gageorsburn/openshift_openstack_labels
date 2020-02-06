package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/hypervisors"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/servers"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

type patchLabelValue struct {
	Op    string            `json:"op"`
	Path  string            `json:"path"`
	Value map[string]string `json:"value"`
}

func main() {

	opts := gophercloud.AuthOptions{
		IdentityEndpoint: "",
		Username:         "",
		Password:         "",
		DomainName:       "default",
	}

	provider, err := openstack.NewClient(opts.IdentityEndpoint)
	if err != nil {
		panic(err)
	}

	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	provider.HTTPClient.Transport = transport

	err = openstack.Authenticate(provider, opts)

	if err != nil {
		panic(err)
	}

	client, err := openstack.NewComputeV2(provider, gophercloud.EndpointOpts{
		Region: "regionOne",
	})

	config, err := clientcmd.BuildConfigFromFlags("", "/Users/gage/.kube/config")
	if err != nil {
		panic(err.Error())
	}

	// create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	pages, err := hypervisors.List(client).AllPages()
	allHypervisors, err := hypervisors.ExtractHypervisors(pages)

	var wg sync.WaitGroup

	for _, hypervisor := range allHypervisors {

		wg.Add(1)

		go func(client *gophercloud.ServiceClient, host string) {
			defer wg.Done()
			pages, err := servers.List(client, servers.ListOpts{
				Host: host,
			}).AllPages()

			if err != nil {
				panic(err)

			}

			allservers, err := servers.ExtractServers(pages)
			if err != nil {
				panic(err)
			}

			for _, server := range allservers {
				// fmt.Printf("Physical: %s, VM: %s, HostID: %s, ProjectID: %s\n", host, server.Name, server.HostID, server.TenantID)

				node, err := clientset.CoreV1().Nodes().Get(server.Name, metav1.GetOptions{})

				// if error is nil that means we found the server in openshift
				if err == nil {

					updateLabel := false

					if value, ok := node.Labels["hypervisor"]; ok {
						if value != host {
							// node has hypervisor label but its not correct anymore so it needs to be updated
							updateLabel = true
						}
					} else {
						// node doesnt have hypervisor label at all
						updateLabel = true
					}

					// fmt.Printf("node %s needs updated: %+v\n", server.Name, updateLabel)

					if updateLabel {
						node.Labels["hypervisor"] = host

						p := []patchLabelValue{
							{
								Op:    "replace",
								Path:  "/metadata/labels",
								Value: node.Labels,
							},
						}

						bs, _ := json.Marshal(p)
						fmt.Printf("%s\n", string(bs))

						_, err = clientset.CoreV1().Nodes().Patch(server.Name, types.JSONPatchType, bs)
						if err != nil {
							panic(err)
						}
					}
				}
			}
		}(client, hypervisor.HypervisorHostname)

	}

	wg.Wait()
}
