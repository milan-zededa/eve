Run test with:

```
EVETEST_LOG_LEVEL=debug EVETEST_NAME=<test-name> make | gotestfmt
```

gotestfmt is just an example of a formatter for the go test output.
It can be installed with:

```
go install github.com/gotesttools/gotestfmt/v2/cmd/gotestfmt@latest
```

Run with separately running broker and a custom adam version:
```
EVETEST_BROKER_ADDRESS=10.10.10.102 EVETEST_ADAM_VERSION=myadam EVETEST_OUTPUT_FORMAT=verbose EVETEST_LOG_LEVEL=debug EVETEST_EVE_VERSION=16.0.0-lts EVETEST_NAME=TestLocalNI make evetest
```

Re-build SDN:
```
LINUXKIT=/home/mlenco/go/src/github.com/lf-edge/eve/build-tools/bin/linuxkit make build-sdn-container
```

Enable IPv6 connectivity on the host:
```
sudo sysctl -w net.ipv6.conf.all.forwarding=1
sudo modprobe ip6table_nat
sudo ip6tables -t nat -A POSTROUTING -o wlp0s20f3 -j MASQUERADE
```

SSH to SDN VM when running with libvirt:
```
ssh -i ~/go/src/github.com/lf-edge/eve/evetest/sdn/vm/cert/ssh/sdn_rsa root@192.168.170.X
```

SSH to SDN VM when running with qemu or libvirt:
```
# docker exec into the evetest container, then:
ssh -i /root/.ssh/sdn_rsa root@250.250.250.2
```

To prevent NetworkManager assigning IPs to xconnect bridges created by libvirt:
```
# This prevents IP assignment, DHCP, etc.
# Create (or edit) a NetworkManager config snippet:
sudo vim /etc/NetworkManager/conf.d/99-evetest-unmanaged.conf

# Add this:
[device-evetest-xconnect-unmanaged]
match-device=interface-name:evetest-x-*
managed=0

# Restart NetworkManager:
sudo systemctl restart NetworkManager

# Verify:
nmcli device status

# And make sure there is no other DHCP client service running.
# I had to for example run:
sudo systemctl disable --now dhcpcd
```

evetest CLI auto-completion:
```
# bash
evetest completion bash > evetest-completion.bash
sudo mv evetest-completion.bash /etc/bash_completion.d/evetest

# zsh
evetest completion zsh > ~/.zsh/completions/_evetest

# fish
evetest completion fish > ~/.config/fish/completions/evetest.fish

# powershell
evetest completion powershell | Out-String | Invoke-Expression
```