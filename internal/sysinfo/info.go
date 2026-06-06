package sysinfo

import (
	"net"
	"os"
	"runtime"
)

type Iface struct {
	Name string   `json:"name"`
	MAC  string   `json:"mac,omitempty"`
	IPs  []string `json:"ips"`
}

type DeviceInfo struct {
	Hostname string  `json:"hostname"`
	OS       string  `json:"os"`
	OSVer    string  `json:"osVer,omitempty"`
	Ifaces   []Iface `json:"ifaces,omitempty"`
}

// Clone returns a deep copy of the DeviceInfo, duplicating the Ifaces and IPs
// slices so the result shares no mutable state with the original. nil-safe.
func (d *DeviceInfo) Clone() *DeviceInfo {
	if d == nil {
		return nil
	}
	out := *d
	if d.Ifaces != nil {
		out.Ifaces = make([]Iface, len(d.Ifaces))
		for i, ifc := range d.Ifaces {
			out.Ifaces[i] = ifc
			out.Ifaces[i].IPs = append([]string(nil), ifc.IPs...)
		}
	}
	return &out
}

// HostnameOverride replaces os.Hostname() when set. The mobile wrapper
// sets this before Collect() runs because Android's kernel hostname is
// "localhost" for all apps.
var HostnameOverride string

func Collect() DeviceInfo {
	hostname := HostnameOverride
	if hostname == "" {
		hostname, _ = os.Hostname()
	}
	return DeviceInfo{
		Hostname: hostname,
		OS:       runtime.GOOS,
		OSVer:    osVersion(),
		Ifaces:   collectIfaces(),
	}
}

func collectIfaces() []Iface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var result []Iface
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		var ips []string
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil {
				continue
			}
			if ip.IsLinkLocalUnicast() || ip.IsLoopback() {
				continue
			}
			ips = append(ips, ip.String())
		}
		if len(ips) == 0 {
			continue
		}
		result = append(result, Iface{
			Name: iface.Name,
			MAC:  iface.HardwareAddr.String(),
			IPs:  ips,
		})
	}
	return result
}
