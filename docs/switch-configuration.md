# Switch configuration

What has to be true on the EOS side before arex can poll anything: eAPI enabled in the right VRF, and a
read-only account that is actually restricted.

## Enable eAPI

eAPI must be enabled in the VRF that carries the management interface. On a switch managed through the `management`
VRF — the usual case — the `vrf management` stanza is required, and connections to the management address are
refused without it.

```text
management api http-commands
   no shutdown
   protocol https
   no protocol http
   !
   vrf management
      no shutdown
!
```

Both levels of `no shutdown` are required, and this is exactly where eAPI departs from `management ssh`. The SSH
idiom — shut the service at the top level, enable it per VRF — does not work here. With only the `vrf` stanza set,
the API stays off entirely:

```text
Enabled: No
HTTPS server: enabled, set to use port 443
VRFs: None
```

The `vrf management` block is present in the running config but inert, and `VRFs: None` is reported regardless. Note
the tell in the HTTPS server line: `enabled` means configured but not listening, whereas `running` means actually
serving. Only the top-level `no shutdown` promotes one to the other. Verified on EOS 4.35.4M.

`protocol https` and `no protocol http` are the defaults on current EOS and can be omitted; they are shown to be
explicit, and to reset a switch where plaintext HTTP was enabled earlier.

If the management interface is in the default VRF, drop the `vrf management` block. If it is in a VRF under another
name, substitute that name — arex itself needs no VRF setting either way, since it connects from off-box.

Confirm the HTTPS server is running and bound to the right VRF:

```text
show management api http-commands
```

Expect `Enabled: Yes`, `HTTPS server: running`, `VRFs: management`, and the management interface listed under
`URLs`. Note the reported `SSL Profile` — a stock switch uses `ARISTA_DEFAULT_SELF_SIGNED_PROFILE`, whose
certificate no client can verify, so set [`tlsSkipVerify`](configuration.md) to `true` on that switch unless you
have installed a certificate signed by a CA that arex trusts.

Then verify eAPI answers from wherever arex will run. This is the same request arex issues:

```bash
curl -k -u prometheus:secret https://<mgmt-ip>/command-api \
  -d '{"jsonrpc":"2.0","method":"runCmds","params":{"version":1,"cmds":["show version"],"format":"json"},"id":1}'
```

## Create a read-only user

arex needs **no elevated privilege**. Every command it issues works at the default privilege level, so do not
grant `privilege 15`:

```text
role prometheus-ro
   10 deny mode config-all command .*
   15 deny command show (running-config|startup-config|tech-support).*
   20 permit command show .*

username prometheus role prometheus-ro secret SHA512 <hash>
```

Rule 15 must precede rule 20: EOS evaluates in sequence and the first match wins, so a broad permit placed first
would swallow it. It covers the `show` commands that dump configuration, which `show .*` would otherwise allow —
`show tech-support` in particular embeds the running configuration. The privilege level already refuses all three,
so this is redundant today; it is here so the role remains safe on its own if the privilege level ever changes.

This role has been verified under enforcement. With command authorization enabled, all seventeen commands arex issues
are permitted, and everything else — including `enable` — is refused:

```text
sw1>enable
% Authorization denied for command 'enable'
```

That refusal is the important one, and it comes from EOS denying any command no rule matches. Rule 10 is therefore
belt-and-braces rather than load-bearing; it is kept because an explicit statement of intent survives someone
later adding a permit rule that turns out to be broader than they meant.

Rule 20 permits every `show` rather than listing the commands arex issues. An exact list would be tighter, but the
command set grows: two commands were added to arex in a single development cycle, and a list-based role would have
started refusing them. `show .*` still permits privileged reads such as `show running-config` — the default
privilege level is what refuses those, which is why the two settings below work together and neither is sufficient
alone.

**A stock switch restricts a monitoring account far less than it appears to**, and the single setting that fixes
it is command authorization:

| | with command authorization | without it (the default) |
| --- | --- | --- |
| role rules | enforced | **ignored entirely** |
| commands matching no rule | denied | permitted |
| `enable` | denied by the role | **elevates with no password** |

Without it, a monitoring account is unrestricted in practice regardless of how careful the role looks. The
privilege level appears to hold — a privilege-1 user is refused `show running-config` — right up until anyone
types `enable`, and eAPI accepts `enable` as a command like any other, so this is reachable in one request over
the network:

```text
sw1>show running-config
% Invalid input (privileged mode required)
sw1>enable
sw1#show running-config
! ... the entire configuration
```

Setting an `enable secret` closes that specific path and is worth doing as defence in depth, since it still holds
if command authorization is ever turned off. But it is not the fix — enabling authorization with a role that
permits only what is needed is.

**Verify.** With authorization enabled and the role above, each of these must be refused:

```bash
# 1. a privileged read, unelevated
curl -k -u prometheus:<password> https://<switch>/command-api \
  -d '{"jsonrpc":"2.0","method":"runCmds","params":{"version":1,"cmds":["show running-config"],"format":"json"},"id":1}'

# 2. the same read, elevating first -- catches a missing enable secret
curl -k -u prometheus:<password> https://<switch>/command-api -d '{"jsonrpc":"2.0","method":"runCmds",
  "params":{"version":1,"cmds":["enable","show running-config"],"format":"json"},"id":1}'

# 3. a command the role forbids -- catches authorization being disabled
curl -k -u prometheus:<password> https://<switch>/command-api \
  -d '{"jsonrpc":"2.0","method":"runCmds","params":{"version":1,"cmds":["configure"],"format":"json"},"id":1}'
```

A refusal looks like this, and the useful part is in `data`, not `message`:

```json
{"code":1002,"message":"CLI command 1 of 1 'show running-config' failed: invalid command",
 "data":[{"errors":["Invalid input (privileged mode required)"]}]}
```

If any of them returns configuration instead, treat the credentials in your arex config as switch administrator
credentials, because that is what they are. They sit in a plaintext file readable by whatever runs arex, and arex
never needs any of that access.

Command authorization has to be enabled globally before role rules are consulted at all; until it is, a role is
inert no matter what it says. Check
`show running-config section aaa`, `show aaa` and `show users accounts detail`. Enabling it starts enforcing roles
for **every** account on every access path at once, including the session you type it in, so do it with a second
privileged session open and a way to roll back.

## Reference configuration

Everything below has been verified on an EOS 4.35.4M switch: eAPI reachable in the management VRF, all nine of
arex's commands permitted, and everything else refused.

```text
management api http-commands
   no shutdown
   protocol https
   no protocol http
   !
   vrf management
      no shutdown
!
aaa authorization exec default local
aaa authorization commands all default local
!
role prometheus-ro
   10 deny mode config-all command .*
   15 deny command show (running-config|startup-config|tech-support).*
   20 permit command show .*
!
username prometheus role prometheus-ro secret sha512 <hash>
```

Apply the `aaa` lines last and with care: they subject **every** account to role checks the moment they commit,
including the session you are typing in. Have a second privileged session open, confirm your administrative
accounts resolve to a role that permits configuration, and keep a way to roll back.

Then confirm the result. As `prometheus`, all of these must be refused:

```text
sw1>enable
% Authorization denied for command 'enable'
sw1>show running-config
% Invalid input (privileged mode required)
sw1>show startup-config
% Invalid input (privileged mode required)
sw1>show tech-support
% Incomplete command (privileged mode required)
```

With rule 15 in place the three configuration dumps are refused by the role instead, so they report
`Authorization denied for command …`. That change in wording is itself the check that rule 15 is working: the
message identifies which control refused, which is worth knowing whenever something unexpected is denied:

| message | mechanism |
| --- | --- |
| `Authorization denied for command …` | command authorization, i.e. the role |
| `Invalid input (privileged mode required)` | privilege level |

`enable` is refused by the role, and the configuration dumps by the privilege level. The two are independent and
both are load-bearing: the role stops elevation, and the privilege level stops privileged reads that `show .*`
would otherwise permit.

Finally, confirm arex itself is unaffected — its role becomes live at the same moment:

```bash
curl -s localhost:9100/metrics | grep arista_command_success
```

All nine at 1. Any at 0 and `-debug` reports the refusal verbatim in its `cause=` field.

---

Back to the [README](../README.md). See also [TLS](tls.md) for verifying the switch certificate.
