---
title: Check router and firewall settings
description: What to check on your home network when automatic remote access can't reach the server.
category: remote
order: 4
audience: Server owners
updated: 2026-10-18
---

Use these steps when automatic router configuration can't make the server reachable. The controls are under **Settings → Server → Remote access**.

## Give the server a stable address

Reserve the server's local address in your router. This keeps a port forward pointing to the right computer and avoids conflicts with the router's address pool.

## Allow the server port

Portico listens on TCP port **32500** by default. HTTP and HTTPS share that port. Allow it through the server's firewall only on the networks you intend to use. Don't turn the firewall off.

If you changed the listener with `PORTICO_BIND`, use that port instead. Docker also needs the port published to the host.

## Forward a port yourself

1. Open **Settings → Server → Remote access → Advanced**.
2. Turn off **Automatic port mapping** if you will maintain the mapping yourself.
3. Set **Public port** to the external TCP port you will forward. For Portico's managed secure address, set **Secure connection port** to that same external port.
4. In your router, forward that external port to the server's stable local address and listener port, normally 32500.
5. Save your settings, then refresh the reachability status in Portico. Test from outside your home Wi-Fi.

With two routers, forward through both or put the upstream router into an appropriate bridge mode. A port forward can't cross your internet provider's carrier-grade NAT. Ask the provider for a reachable public address, use reachable IPv6 with suitable firewall rules, or provide a route using a VPN or reverse proxy.

## Use a reverse proxy or a custom address

Portico supports a reverse proxy at its own hostname, such as `https://media.example.com`. Hosting it under a path such as `/portico/` isn't supported.

1. Point the proxy at the Portico listener.
2. Under **Access from outside**, enter the public origin in **External address**. Use **Extra addresses** for additional origins. An origin contains a scheme, hostname and optional port, with no path, query or credentials.
3. Add only the proxy's address or range to **Trusted reverse proxies**.
4. Configure the proxy to replace client-supplied forwarding headers. Preserve `Host` or set `X-Forwarded-Host`, set `X-Forwarded-Proto`, and pass the real client address in `X-Forwarded-For`. Portico also accepts RFC `Forwarded` headers from trusted proxies.
5. Save and test the external address.

An HTTPS proxy normally accepts clients on port 443 and forwards to Portico on 32500. Use the actual ports you configured. Only HTTPS addresses are published through Portico Services.

## Use your own certificate

To terminate HTTPS in Portico itself, fill in **Domain**, **Certificate file**, and **Key file** under **Your own certificate**. Supply a PEM certificate chain and matching unencrypted PEM private key that the server service account can read. A Portico Account isn't required for this option.

Portico apps can trust your own certificate, including a self-signed certificate, after verifying your server's identity. Browsers use their own certificate trust rules.

## Connect through a VPN

You can connect directly to an address reachable through your VPN. The VPN must provide the route; Portico doesn't detect or create it. **Home networks** decides whether Portico treats an address as local for sign-in and streaming limits. Changing that list doesn't open a route or a firewall.

Plain HTTP is accepted by the server from any address. Apple apps allow it only to local-network addresses; use HTTPS for a remote public address. Other platforms allow HTTP from anywhere. Use HTTPS when traffic crosses an untrusted network.

If Docker, a proxy or a tunnel hides every client's address behind one gateway, pass the real address through a trusted HTTP proxy or remove that gateway's range from **Home networks**. Otherwise remote clients can be treated as local.
