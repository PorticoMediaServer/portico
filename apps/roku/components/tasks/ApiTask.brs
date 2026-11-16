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

function newTransfer(url as string) as object
    x = createObject("roUrlTransfer")
    x.setMessagePort(m.port)
    x.setUrl(url)
    x.retainBodyOnError(true)
    x.enableEncodings(true)
    if left(url, 5) = "https"
        x.setCertificatesFile("common:/certs/ca-bundle.crt")
        x.initClientCertificates()
    end if
    x.addHeader("Accept", "application/json")
    x.addHeader("Content-Type", "application/json")
    return x
end function

sub send(req as object, retried as boolean)
    s = m.top.session
    if s = invalid or s.origin = invalid
        answer(req, {ok: false, status: 0, error: {code: "no_server", message: "No server is chosen."}})
        return
    end if
    if m.renewing and req.public <> true
        m.waiting.push(req)
        return
    end if
    x = newTransfer(s.origin + req.path)
    if req.public <> true and s.accessToken <> invalid then x.addHeader("Authorization", "Bearer " + s.accessToken)
    method = req.method
    body = ""
    if req.body <> invalid then body = formatJson(req.body)
    started = false
    if method = "GET"
        started = x.asyncGetToString()
    else
        if method = "POST"
            x.addHeader("Idempotency-Key", createObject("roDeviceInfo").getRandomUUID())
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
    if status >= 200 and status < 300
        answer(p.req, {ok: true, status: status, data: data})
    else
        err = {code: "unreachable", message: "The server could not be reached.", retry: true}
        if status > 0 then err = {code: "http_" + status.toStr(), message: "The server could not do that.", retry: false}
        if data <> invalid and data.error <> invalid then err = data.error
        answer(p.req, {ok: false, status: status, error: err})
    end if
end sub

sub answer(req as object, result as object)
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
    m.renewing = true
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
    else if status = 401 or status = 400 or status = 403
        storeWrite({renewalId: ""})
        for each req in held
            answer(req, {ok: false, status: 401, error: {code: "sign_in_ended", message: "Sign in again."}})
        end for
        ended()
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
