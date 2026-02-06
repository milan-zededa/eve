802.1x Container Testbed
========================

Diagram:

```
+----------------------------+                   +----------------------------------------------------------+
|                 +-------+  |  192.168.50.0/24  |   +-------+   +-----+                                    |
| wpa-supplicant  | veth1 *--------------------------* veth0 |---| br0 |  hostapd                           |
| dhclient        +-------+  |                   |   +-------+   +-----+                                    |
|                            |                   |                 |                                        |
| scep-client                |                   |                 |  +-----------+                         |
|  +-------+                 |                   |                 +--* br0.100   |  dnsmasq (DHCP server)  |
|  | veth2 |                 |                   |                 |  | (no-auth) |   192.168.100.0/24      |
|  +---*---+                 |                   |                 |  +-----------+                         |
+------|---------------------+                   |                 |                                        |
       |                                         |                 |  +-----------+                         |
       |                                         |                 +--* br0.200   |  dnsmasq (DHCP server)  |
       |                                         |                    |  (auth)   |   192.168.200.0/24      |
       |                                         |                    +-----------+                         |
       |  192.168.60.0/24                        |                                                          |
       |                                         +----------------------------------------------------------+
       |
+------|---------------------+                   +-------------------------+
|  +---*---+      +-------+  |  192.168.70.0/24  |   +-------+             |
|  | veth3 |      | veth4 *--------------------------* veth5 |  scepserver |
|  +-------+      +-------+  |                   |   +-------+             |
|         scep-proxy         |                   |                         |
+----------------------------+                   +-------------------------+
```

Workflow:

```
1. Build containers
$ make build

2. Run the Testbed
$ make run-lab

3. Try to obtain IP address for un-authenticated VLAN (192.168.100.0/24)
$ make enter-sup
$ get-ip.sh veth1
...
[veth1] DHCP lease acquired — gateway is 192.168.100.1
[veth1] Default route set via 192.168.100.1

4. Check Internet access (should be blocked).
$ make enter-sup
$ curl google.com >/dev/null 2>&1; echo $?
6

5. Try to get a (first) client certificate from the SCEP server through the SCEP proxy
$ make enter-sup
$ run-scep-client.sh --with-proxy
...
level=info ts=2025-12-19T10:45:09.51756026Z pkiStatus=SUCCESS msg="server returned a certificate."
...
$ openssl x509 -in /etc/certs/pnac-client.pem -text -noout
Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number: 2 (0x2)
        Signature Algorithm: sha256WithRSAEncryption
        Issuer: CN = pnac-Test CA, OU = Lab, O = Example, C = US
        Validity
            Not Before: Dec 19 10:35:09 2025 GMT
            Not After : Dec 19 10:45:09 2026 GMT
        Subject: C = US, O = scep-client, OU = MDM, CN = scepclient
...

6. Start the 802.1x supplicant
$ make enter-sup
$ run-supplicant.sh
...
EAPOL: SUPP_PAE entering state AUTHENTICATED
...
EAPOL authentication completed - result=SUCCESS
[veth1] WPA EVENT: Connected
...

7. Use hostapd_cli on the authenticator side to confirm
$ make enter-auth
$ hostapd_cli -p /var/run/hostapd -i br0 all_sta | head -n 2
3a:dc:47:6d:92:60
flags=[AUTHORIZED]
$ bridge vlan show
port              vlan-id
br0               1 PVID Egress Untagged
                  100
                  200
veth0             1 Egress Untagged
                  200 PVID Egress Untagged

8. Try to obtain IP address for authenticated VLAN (192.168.200.0/24)
$ make enter-sup
$ get-ip.sh veth1
...
[veth1] DHCP lease acquired — gateway is 192.168.200.1
[veth1] Default route set via 192.168.200.1

9. Check Internet access (should be allowed)
$ make enter-sup
$ curl google.com >/dev/null 2>&1; echo $?
0

10. Try to get a new certificate from the SCEP server through the SCEP proxy
   (private key generated in step 4 is reused)
$ make enter-sup
$ run-scep-client.sh --with-proxy
...
level=info ts=2025-12-19T10:49:31.441146171Z pkiStatus=SUCCESS msg="server returned a certificate."
...
$ openssl x509 -in /etc/certs/pnac-client.pem -text -noout
Certificate:
    Data:
        Version: 3 (0x2)
        Serial Number: 3 (0x3)
        Signature Algorithm: sha256WithRSAEncryption
        Issuer: CN = pnac-Test CA, OU = Lab, O = Example, C = US
        Validity
            Not Before: Dec 19 10:39:31 2025 GMT
            Not After : Dec 19 10:49:31 2026 GMT
        Subject: C = US, O = scep-client, OU = MDM, CN = scepclient
...

11. Re-run port authentication with the new certificate
$ make enter-auth
$ deauthenticate.sh

$ make enter-sup
# You can get new DHCP lease and try Internet access at this point -- it should be blocked.
$ get-ip.sh veth1
...
[veth1] DHCP lease acquired — gateway is 192.168.100.1
[veth1] Default route set via 192.168.100.1
$ curl google.com >/dev/null 2>&1; echo $?
6
$ rerun-supplicant.sh
...
EAPOL: SUPP_PAE entering state AUTHENTICATED
...
$ get-ip.sh veth1
...
[veth1] DHCP lease acquired — gateway is 192.168.200.1
[veth1] Default route set via 192.168.200.1
$ curl google.com >/dev/null 2>&1; echo $?
0

12. Tear down and cleanup everything
make clean
```