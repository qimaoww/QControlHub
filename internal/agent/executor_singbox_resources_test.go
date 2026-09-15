package agent

import (
	"testing"
)

func TestValidateNoRelativeSingBoxResourcesContract(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		contents string
		wantErr  bool
	}{
		"relative clash external ui": {
			contents: `{"experimental":{"clash_api":{"external_ui":"dashboard"}}}`,
			wantErr:  true,
		},
		"absolute clash external ui": {
			contents: `{"experimental":{"clash_api":{"external_ui":"/srv/sing-box/dashboard"}}}`,
			wantErr:  false,
		},
		"relative acme data directory": {
			contents: `{"inbounds":[{"type":"trojan","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"tls":{"enabled":true,"certificate_path":"/etc/cert.pem","key_path":"/etc/key.pem","acme":{"data_directory":"acme-data"}}}]}`,
			wantErr:  true,
		},
		"absolute acme data directory": {
			contents: `{"inbounds":[{"type":"trojan","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"tls":{"enabled":true,"certificate_path":"/etc/cert.pem","key_path":"/etc/key.pem","acme":{"data_directory":"/var/lib/acme"}}}]}`,
			wantErr:  false,
		},
		"relative client certificate path array": {
			contents: `{"inbounds":[{"type":"trojan","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"tls":{"enabled":true,"certificate_path":"/etc/cert.pem","key_path":"/etc/key.pem","client_certificate_path":["client.pem","ca.pem"]}}]}`,
			wantErr:  true,
		},
		"absolute client certificate path array": {
			contents: `{"inbounds":[{"type":"trojan","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"tls":{"enabled":true,"certificate_path":"/etc/cert.pem","key_path":"/etc/key.pem","client_certificate_path":["/etc/client.pem","/etc/ca.pem"]}}]}`,
			wantErr:  false,
		},
		"relative inbound certificate path": {
			contents: `{"inbounds":[{"type":"trojan","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"tls":{"enabled":true,"certificate_path":"cert.pem","key_path":"/etc/key.pem"}}]}`,
			wantErr:  true,
		},
		"relative outbound ech config path": {
			contents: `{"outbounds":[{"type":"vless","server":"example.com","server_port":443,"uuid":"abc","tls":{"enabled":true,"ech":{"config_path":"ech.json"}}}]}`,
			wantErr:  true,
		},
		"relative local rule set path": {
			contents: `{"route":{"rule_set":[{"type":"local","tag":"geo","format":"binary","path":"ruleset.srs"}]}}`,
			wantErr:  true,
		},
		"absolute local rule set path": {
			contents: `{"route":{"rule_set":[{"type":"local","tag":"geo","format":"binary","path":"/etc/sing-box/ruleset.srs"}]}}`,
			wantErr:  false,
		},
		"relative ssh private key": {
			contents: `{"outbounds":[{"type":"ssh","server":"example.com","server_port":22,"user":"root","private_key_path":"id_ed25519"}]}`,
			wantErr:  true,
		},
		"relative tor executable": {
			contents: `{"outbounds":[{"type":"tor","server":"127.0.0.1","server_port":9050,"executable_path":"./tor"}]}`,
			wantErr:  true,
		},
		"relative dialer protect path": {
			contents: `{"outbounds":[{"type":"direct","protect_path":"protect.sock"}]}`,
			wantErr:  true,
		},
		"relative geoip path": {
			contents: `{"route":{"geoip":{"path":"geoip.db"}}}`,
			wantErr:  true,
		},
		"relative cache file path": {
			contents: `{"experimental":{"cache_file":{"enabled":true,"path":"cache.db"}}}`,
			wantErr:  true,
		},
		"log output stdout accepted": {
			contents: `{"log":{"output":"stdout"}}`,
			wantErr:  false,
		},
		"log output stderr accepted": {
			contents: `{"log":{"output":"stderr"}}`,
			wantErr:  false,
		},
		"log output relative deferred to import boundary": {
			contents: `{"log":{"output":"relative.log"}}`,
			wantErr:  false,
		},
		"log output absolute accepted": {
			contents: `{"log":{"output":"/var/log/sing-box.log"}}`,
			wantErr:  false,
		},
		"http transport url path not treated as file": {
			contents: `{"outbounds":[{"type":"http","server":"example.com","server_port":8080,"path":"/proxy"}]}`,
			wantErr:  false,
		},
		"relative certificate directory path": {
			contents: `{"certificate":{"certificate_directory_path":["certs"]}}`,
			wantErr:  true,
		},
		"absolute certificate directory path": {
			contents: `{"certificate":{"certificate_directory_path":["/etc/sing-box/certs"]}}`,
			wantErr:  false,
		},
		"relative certificate path list": {
			contents: `{"certificate":{"certificate_path":["cert.pem"]}}`,
			wantErr:  true,
		},
		"absolute certificate path list": {
			contents: `{"certificate":{"certificate_path":["/etc/sing-box/cert.pem"]}}`,
			wantErr:  false,
		},
		"relative tailscale state directory": {
			contents: `{"endpoints":[{"type":"tailscale","tag":"ts","state_directory":"state"}]}`,
			wantErr:  true,
		},
		"absolute tailscale state directory": {
			contents: `{"endpoints":[{"type":"tailscale","tag":"ts","state_directory":"/var/lib/tailscale"}]}`,
			wantErr:  false,
		},
		"relative ccm credential path": {
			contents: `{"services":[{"type":"ccm","tag":"ccm","credential_path":"creds.json"}]}`,
			wantErr:  true,
		},
		"absolute ccm credential path": {
			contents: `{"services":[{"type":"ccm","tag":"ccm","credential_path":"/etc/sing-box/creds.json"}]}`,
			wantErr:  false,
		},
		"relative ocm usages path": {
			contents: `{"services":[{"type":"ocm","tag":"ocm","usages_path":"usages.json"}]}`,
			wantErr:  true,
		},
		"absolute ocm usages path": {
			contents: `{"services":[{"type":"ocm","tag":"ocm","usages_path":"/etc/sing-box/usages.json"}]}`,
			wantErr:  false,
		},
		"relative derp config path": {
			contents: `{"services":[{"type":"derp","tag":"derp","config_path":"derp.json"}]}`,
			wantErr:  true,
		},
		"absolute derp config path": {
			contents: `{"services":[{"type":"derp","tag":"derp","config_path":"/etc/sing-box/derp.json"}]}`,
			wantErr:  false,
		},
		"relative derp mesh psk file": {
			contents: `{"services":[{"type":"derp","tag":"derp","mesh_psk_file":"psk.txt"}]}`,
			wantErr:  true,
		},
		"absolute derp mesh psk file": {
			contents: `{"services":[{"type":"derp","tag":"derp","mesh_psk_file":"/etc/sing-box/psk.txt"}]}`,
			wantErr:  false,
		},
		"relative ssmapi cache path": {
			contents: `{"services":[{"type":"ssm-api","tag":"ssm","cache_path":"cache.db"}]}`,
			wantErr:  true,
		},
		"absolute ssmapi cache path": {
			contents: `{"services":[{"type":"ssm-api","tag":"ssm","cache_path":"/var/lib/sing-box/cache.db"}]}`,
			wantErr:  false,
		},
		"relative hysteria2 masquerade file directory": {
			contents: `{"inbounds":[{"type":"hysteria2","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"masquerade":{"type":"file","directory":"webroot"}}]}`,
			wantErr:  true,
		},
		"absolute hysteria2 masquerade file directory": {
			contents: `{"inbounds":[{"type":"hysteria2","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"masquerade":{"type":"file","directory":"/var/www/sing-box"}}]}`,
			wantErr:  false,
		},
		"relative hysteria2 masquerade file url": {
			contents: `{"inbounds":[{"type":"hysteria2","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"masquerade":"file:webroot"}]}`,
			wantErr:  true,
		},
		"absolute hysteria2 masquerade file url": {
			contents: `{"inbounds":[{"type":"hysteria2","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"masquerade":"file:///var/www/sing-box"}]}`,
			wantErr:  false,
		},
		"http hysteria2 masquerade url accepted": {
			contents: `{"inbounds":[{"type":"hysteria2","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"masquerade":"https://example.com/ui"}]}`,
			wantErr:  false,
		},
		"unknown masquerade scheme left to core": {
			contents: `{"inbounds":[{"type":"hysteria2","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"masquerade":"ftp://example.com/ui"}]}`,
			wantErr:  false,
		},
		"future resource named path rejected by fallback": {
			contents: `{"inbounds":[{"type":"hysteria2","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"some_future_path":"data.db"}]}`,
			wantErr:  true,
		},
		"future resource named file rejected by fallback": {
			contents: `{"inbounds":[{"type":"hysteria2","listen":"127.0.0.1","listen_port":443,"users":[{"password":"testpw"}],"some_future_file":"data.db"}]}`,
			wantErr:  true,
		},
		"rule process path matching not treated as resource": {
			contents: `{"route":{"rules":[{"type":"default","process_path":"sbin/nginx","action":"direct"}]}}`,
			wantErr:  false,
		},
		"http transport relative path not treated as resource": {
			contents: `{"outbounds":[{"type":"http","server":"example.com","server_port":8080,"path":"proxy"}]}`,
			wantErr:  false,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := validateNoRelativeSingBoxResources(tt.contents)
			if tt.wantErr && err == nil {
				t.Fatalf("validateNoRelativeSingBoxResources() accepted a relative resource:\n%s", tt.contents)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateNoRelativeSingBoxResources() rejected an absolute resource: %v\n%s", err, tt.contents)
			}
		})
	}
}
