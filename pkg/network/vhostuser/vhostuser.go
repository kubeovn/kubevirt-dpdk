package vhostuser

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	v1 "kubevirt.io/api/core/v1"

	"kubevirt.io/kubevirt/pkg/network/vmispec"
)

const (
	VhostuserSocketDirMountPath  = "/var/run/vm/dpdk/"
	VhostuserSocketDirVolumeName = "vhostuser-sockets"

	OvsRunDirMountPath  = "/var/run/openvswitch/"
	OvsRunDirVolumeName = "ovs-run-dir"

	AnnotKeyNetworkStatus                = "k8s.v1.cni.cncf.io/network-status"
	NetworkStatusMapMountPath            = "/etc/podnetinfo"
	NetworkStatusMapAnnotationVolumeName = "networks-status-map-annotation"
	NetworkStatusMapVolumePath           = "networks-status-map"
)

type NetworkStatus struct {
	Name      string   `json:"name"`
	Interface string   `json:"interface,omitempty"`
	IPs       []string `json:"ips,omitempty"`
	Mac       string   `json:"mac,omitempty"`
}

type VhostUserNeedInfo struct {
	Mac           string
	InterfaceName string
}

func GetDpdkInterfaceInfo(vmi *v1.VirtualMachineInstance) (map[string]*VhostUserNeedInfo, error) {
	vhostuserIfaces := vmispec.FilterVhostuserInterfaces(vmi.Spec.Domain.Devices.Interfaces)
	if len(vhostuserIfaces) == 0 {
		return nil, nil
	}
	vhostuserNets := vmispec.FilterNetworksByInterfaces(vmi.Spec.Networks, vhostuserIfaces)
	networkStatus, err := newMultusNetworkStatusFromFile()
	if err != nil {
		return nil, err
	}
	return getDpdkInterfaceInfo(vmi.GetNamespace(), vhostuserNets, networkStatus)
}

func getDpdkInterfaceInfo(namespace string, vhostuserNets []v1.Network, networkStatus []NetworkStatus) (map[string]*VhostUserNeedInfo, error) {
	var dpdkInterfaceInfo = make(map[string]*VhostUserNeedInfo)
	for _, network := range vhostuserNets {
		nadName := network.Multus.NetworkName
		nadNamespaceWithName := network.Multus.NetworkName
		nadSlice := strings.Split(network.Multus.NetworkName, "/")
		if len(nadSlice) != 2 {
			nadNamespaceWithName = fmt.Sprintf("%s/%s", namespace, nadName)
		} else {
			nadName = nadSlice[1]
		}
		var mac string
		var interfaceName string
		for _, ns := range networkStatus {
			if ns.Name == nadNamespaceWithName {
				mac = ns.Mac
				interfaceName = ns.Interface
				break
			}
		}
		if len(mac) == 0 || len(interfaceName) == 0 {
			return nil, fmt.Errorf("vhostuser net %s mac or interfaceName not found", network.Name)
		}
		dpdkInterfaceInfo[nadName] = &VhostUserNeedInfo{
			Mac:           mac,
			InterfaceName: interfaceName,
		}
	}
	return dpdkInterfaceInfo, nil
}

func newMultusNetworkStatusFromFile() ([]NetworkStatus, error) {
	netStatusPath := path.Join(NetworkStatusMapMountPath, NetworkStatusMapVolumePath)
	networkStatusMapBytes, err := readFileUntilNotEmpty(netStatusPath)
	if err != nil {
		if isFileEmptyAfterTimeout(err, networkStatusMapBytes) {
			return nil, err
		}
		return nil, nil
	}

	return newMultusNetworkStatus(networkStatusMapBytes)
}

func readFileUntilNotEmpty(networkStatusMapPath string) ([]byte, error) {
	var networkStatusMapBytes []byte
	err := wait.PollImmediate(100*time.Millisecond, time.Second, func() (bool, error) {
		var err error
		networkStatusMapBytes, err = os.ReadFile(networkStatusMapPath)
		return len(networkStatusMapBytes) > 0, err
	})
	return networkStatusMapBytes, err
}

func isFileEmptyAfterTimeout(err error, data []byte) bool {
	return errors.Is(err, wait.ErrWaitTimeout) && len(data) == 0
}

func newMultusNetworkStatus(networkStatusMapBytes []byte) ([]NetworkStatus, error) {
	var networkStatusMap []NetworkStatus
	err := json.Unmarshal(networkStatusMapBytes, &networkStatusMap)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal network-status map %w", err)
	}

	return networkStatusMap, nil
}
