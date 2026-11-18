sub init()
    m.top.functionName = "serve"
end sub

sub serve()
    m.port = createObject("roMessagePort")
    m.top.observeField("request", m.port)
    m.pending = {}      ' transfer identity -> {xfer, req, retried}
    m.waiting = []      ' requests held while the sign-in is renewed
    m.renewing = false
    m.top.ready = true
    m.clock = createObject("roTimespan")
    while true
        msg = wait(1000, m.port)
        t = type(msg)
        if t = "roSGNodeEvent"
            send(msg.getData(), false)
        else if t = "roUrlEvent"
            finish(msg)
        end if
        giveUpOnSilent()
    end while
end sub

' Roku's transfers have no time limit of their own. One the server has not answered in 20 seconds
' is cancelled and counts as out of reach, so nothing (a renewal least of all) waits for ever.
sub giveUpOnSilent()
    now = m.clock.totalMilliseconds()
    for each key in m.pending.keys()
        p = m.pending[key]
        if now - p.started > 20000
            p.xfer.asyncCancel()
            m.pending.delete(key)
            if p.renewal = true
                renewed(0, invalid)
            else
                answer(p.req, {ok: false, status: 0, error: {code: "unreachable", message: "The server could not be reached.", retry: true}})
            end if
        end if
    end for
end sub

sub send(req as object, retried as boolean)
    s = m.top.session
    ' A sign-in request names its own server; everything else goes to the one signed in to.
    origin = req.origin
    if origin = invalid or origin = ""
        if s = invalid or s.origin = invalid
            answer(req, {ok: false, status: 0, error: {code: "no_server", message: "No server is chosen."}})
            return
        end if
        origin = s.origin
    end if
    if m.renewing and req.public <> true
        m.waiting.push(req)
        return
    end if
    x = newTransfer(origin + req.path)
    if req.token <> invalid and req.token <> ""
        x.addHeader("Authorization", "Bearer " + req.token)
    else if req.public <> true and s <> invalid and s.accessToken <> invalid
        x.addHeader("Authorization", "Bearer " + s.accessToken)
    end if
    if req.ifMatch <> invalid and req.ifMatch <> "" then x.addHeader("If-Match", req.ifMatch)
    method = req.method
    body = ""
    if req.body <> invalid then body = formatJson(req.body)
    started = false
    if method = "GET"
        started = x.asyncGetToString()
    else
        if method = "POST"
            key = req.key
            if key = invalid or key = "" then key = createObject("roDeviceInfo").getRandomUUID()
            x.addHeader("Idempotency-Key", key)
        else
            x.setRequest(method)
        end if
        started = x.asyncPostFromString(body)
    end if
    if not started
        answer(req, {ok: false, status: 0, error: {code: "unreachable", message: "The server could not be reached.", retry: true}})
        return
    end if
    m.pending[x.getIdentity().toStr()] = {xfer: x, req: req, retried: retried, started: m.clock.totalMilliseconds()}
end sub

sub finish(msg as object)
    key = msg.getSourceIdentity().toStr()
    p = m.pending[key]
    if p = invalid then return
    m.pending.delete(key)
    status = msg.getResponseCode()
    text = msg.getString()
    data = invalid
    if text <> invalid and text <> "" then data = parseJson(text)
    if p.check = true
        checked(status, data)
        return
    end if
    if p.renewal = true
        renewed(status, data)
        return
    end if
    if status = 401 and p.req.public <> true and not p.retried
        ' The access token ran out: renew once, then send this again.
        p.req.retriedAfterRenewal = true
        m.waiting.push(p.req)
        renew()
        return
    end if
    if status >= 200 and status < 300 and p.req.path = "/v1/servers?signature=rsa" and not listHolds(data)
        ' Portico Services' list of the account's servers must hold its second signature (roRSA works only on
        ' a task's thread, so it is checked here): the screens never see a list that does not
        answer(p.req, {ok: false, status: status, error: {code: "list_unchecked", message: "This TV could not check the list of servers from Portico Services. Try again later.", retry: true}})
    else if status >= 200 and status < 300
        answer(p.req, {ok: true, status: status, data: data})
    else
        err = {code: "unreachable", message: "The server could not be reached.", retry: true}
        if status > 0 then err = {code: "http_" + status.toStr(), message: "The server could not do that.", retry: false}
        if data <> invalid and data.error <> invalid then err = data.error
        answer(p.req, {ok: false, status: status, error: err})
    end if
end sub

sub answer(req as object, result as object)
    if not result.ok
        ' the app's log: the route, the status, the server's error code and its request id
        said = req.method + " " + req.path + " answered " + result.status.toStr()
        if result.error <> invalid and result.error.code <> invalid then said = said + " " + result.error.code
        if result.error <> invalid and result.error.requestId <> invalid then said = said + ", request " + result.error.requestId
        logApp("warn", "network", said)
    end if
    if req.reply <> invalid then req.reply.result = result
end sub

' Renewal follows the server's rule: the request id is saved before it is sent, so a renewal
' whose answer was lost is asked again with the same id and gets the same new token.
sub renew()
    if m.renewing then return
    s = m.top.session
    if s = invalid or s.refreshToken = invalid or s.refreshToken = ""
        ended()
        return
    end if
    ' A sign-in kept before the server's id was: sign in again (nothing in the field holds one).
    if s.serverId = invalid or s.serverId = ""
        ended()
        return
    end if
    m.renewing = true
    ' Before the refresh token goes anywhere, the address is asked who it is (security finding F9,
    ' setup rules 68 and 71). This catches "the address is now another machine or another
    ' server": a home address handed to another computer, a server reinstalled with a new
    ' identity. It does NOT stop an impostor that copies the id from the real server's answer:
    ' the real proof (the server signing a challenge, /v1/system/identity-proof, Ed25519) cannot
    ' be checked in BrightScript, which has no such primitive, and on a plain-HTTP home address
    ' nothing on a Roku can tell a stand-in from the server. The full identity check and trust
    ' in a server's private certificate stay open (the lane's item 4, owner question 16).
    x = newTransfer(s.origin + "/v1/system")
    x.asyncGetToString()
    m.pending[x.getIdentity().toStr()] = {xfer: x, renewal: true, check: true, started: m.clock.totalMilliseconds()}
end sub

' What the address answered: "same" (the server signed in to), "other" (another Portico server),
' "notPortico" (something answers, not as Portico) or "unreachable" (no answer, or a server error).
function serverAnswer(status as integer, data as dynamic, serverId as string) as string
    if status = 0 or status >= 500 then return "unreachable"
    if status <> 200 or type(data) <> "roAssociativeArray" or getInterface(data.id, "ifString") = invalid or data.apiMajor = invalid then return "notPortico"
    if data.id = serverId then return "same"
    return "other"
end function

sub checked(status as integer, data as dynamic)
    who = serverAnswer(status, data, m.top.session.serverId)
    if who = "same"
        sendRenewal()
    else if who = "unreachable"
        renewed(0, invalid)
    else
        ' Not the server signed in to: the refresh token is not sent, and is kept, so the viewer
        ' can try again once the address is right. Waiting requests get the reason.
        m.renewing = false
        held = m.waiting
        m.waiting = []
        for each req in held
            answer(req, {ok: false, status: 0, error: {code: "server_changed", message: "This address no longer answers as your server.", retry: true}})
        end for
        m.top.serverChanged = who
    end if
end sub

sub sendRenewal()
    s = m.top.session
    requestId = storeRead("renewalId")
    if requestId = ""
        requestId = createObject("roDeviceInfo").getRandomUUID()
        storeWrite({renewalId: requestId})
    end if
    x = newTransfer(s.origin + "/v1/auth/refresh")
    x.addHeader("Idempotency-Key", requestId)
    x.asyncPostFromString(formatJson({refreshToken: s.refreshToken, installationId: s.installationId, requestId: requestId}))
    m.pending[x.getIdentity().toStr()] = {xfer: x, renewal: true, started: m.clock.totalMilliseconds()}
end sub

sub renewed(status as integer, data as dynamic)
    m.renewing = false
    held = m.waiting
    m.waiting = []
    if status = 200 and data <> invalid and data.accessToken <> invalid
        s = m.top.session
        s.accessToken = data.accessToken
        if data.refreshToken <> invalid then s.refreshToken = data.refreshToken
        storeWrite({refreshToken: s.refreshToken, renewalId: ""})
        m.top.session = s
        for each req in held
            send(req, true)
        end for
    else if status = 401 or status = 400
        storeWrite({renewalId: ""})
        for each req in held
            answer(req, {ok: false, status: 401, error: {code: "sign_in_ended", message: "Sign in again."}})
        end for
        ended()
    else if status = 403 and data <> invalid and data.error <> invalid
        ' Not now (outside this account's viewing hours, or away from home): the sign-in stands
        ' and the server's own sentence says why. The same renewal is asked again later.
        for each req in held
            answer(req, {ok: false, status: 403, error: data.error})
        end for
    else
        ' The server was out of reach: the sign-in stands, the waiting requests fail as unreachable.
        for each req in held
            answer(req, {ok: false, status: 0, error: {code: "unreachable", message: "The server could not be reached.", retry: true}})
        end for
    end if
end sub

sub ended()
    m.top.signInEnded = true
end sub

' --- The account's server list: its RSA signature, then its rules; what holds is pinned (_scratch/roku/ACCOUNT-SIGNATURE.md)

function listHolds(data as dynamic) as boolean
    if data = invalid or data.list = invalid or data.listRsa = invalid then return false
    known = false
    for each id in listKeyIds()
        if data.listRsa.keyId = id then known = true
    end for
    if not known or data.list.payload = invalid or data.listRsa.signature = invalid then return false
    payload = base64urlBytes(data.list.payload)
    rsa = createObject("roRSA")
    if rsa = invalid or rsa.SetPublicKey("pkg:/source/services-list-" + data.listRsa.keyId + ".pem") <> 1 then return false
    rsa.SetDigestAlgorithm("sha256")
    if rsa.Verify(digestOf(payload), base64urlBytes(data.listRsa.signature)) <> 1 then return false
    list = parseJson(payload.toAsciiString())
    held = invalid
    if storeRead("porticoListVersion") <> "" then held = storeRead("porticoListVersion").toFloat()
    if listRules(list, held, serverIdOf) <> "" then return false
    ' pinned: the version (a lower one is refused from now on) and each server's key fingerprint, which a
    ' server found on the home network must announce (Use a local server)
    prints = {}
    for each server in list.servers
        prints[server.serverId] = bytesBase64url(digestOf(base64urlBytes(server.publicKey)))
    end for
    storeWrite({porticoListVersion: list.version.toStr(), porticoPrints: formatJson(prints)})
    return true
end function

function digestOf(bytes as object) as object
    digest = createObject("roEVPDigest")
    digest.Setup("sha256")
    out = createObject("roByteArray")
    out.fromHexString(digest.Process(bytes))
    return out
end function

' A server's id: srv_ + base64url(SHA-256("portico.server.identity.v1" 0x00 + its public key)).
function serverIdOf(publicKey as string) as string
    bytes = createObject("roByteArray")
    bytes.fromAsciiString("portico.server.identity.v1")
    bytes.push(0)
    bytes.append(base64urlBytes(publicKey))
    return "srv_" + bytesBase64url(digestOf(bytes))
end function
