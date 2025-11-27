//go:build !custom || inputs || inputs.ssh_guard

package all

import _ "github.com/influxdata/telegraf/plugins/inputs/ssh_guard" // register plugin
