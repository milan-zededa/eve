802.1x Container Testbed
========================

Diagram:

```
+----------------------------+                   +------------------------------------+
|                 +-------+  |  192.168.50.0/24  |   +-------+                        |
| wpa-supplicant  | veth1 *--------------------------* veth0 |  hostapd               |
| dhclient        +-------+  |                   |   +-------+  dnsmasq (DHCP server) |
|                            |                   |                                    |
| scep-client                |                   +------------------------------------+
|  +-------+                 |
|  | veth2 |                 |
|  +---*---+                 |
+------|---------------------+
       |
       |  192.168.60.0/24
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

3. Try to obtaine IP address for the veth inside the supplicant container (should fail, port not authenticated) 
$ make enter-sup
$ dhclient -d -v veth1
...
DHCPDISCOVER on veth1 to 255.255.255.255 port 67 interval 3 (xid=0x861df24)
... (never receives any offer back)

4. Try to get a (first) client certificate from the SCEP server through the SCEP proxy
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

5. Start the 802.1x supplicant (+ DHCP client after successful authentication)
$ make enter-sup
$ run-supplicant.sh
...
EAPOL: SUPP_PAE entering state AUTHENTICATED
...
DHCPREQUEST for 192.168.50.103 on veth1 to 255.255.255.255 port 67 (xid=0x2fece0b0)
DHCPACK of 192.168.50.103 from 192.168.50.1 (xid=0xb0e0ec2f)
...

6. Use hostapd_cli on the authenticator side to confirm
$ make enter-auth
$ hostapd_cli -p /var/run/hostapd -i veth0 all_sta | head -n 2
3a:dc:47:6d:92:60
flags=[AUTHORIZED]

7. Check Internet access (default route should point to veth1 at this point)
$ make enter-sup
$ curl google.com >/dev/null 2>&1; echo $?
0

8. Try to get a new certificate from the SCEP server through the SCEP proxy
   (private generate in step 4 is reused)
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

9. Re-run port authentication with the new certificate
$ make enter-auth
$ deauthenticate.sh

$ make enter-sup
# You can try Internet access at this point -- it should be blocked.
$ curl google.com >/dev/null 2>&1; echo $?
6
$ rerun-supplicant.sh
...
EAPOL: SUPP_PAE entering state AUTHENTICATED
...
$ curl google.com >/dev/null 2>&1; echo $?
0

10. Tear down and cleanup everything
make clean
```