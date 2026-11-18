' The renewal's server check (security finding F9) on the task's own code (components/tasks/ApiTask.brs)
' with stand-ins for the network and the store: the refresh token goes only to the server whose id
' was kept at sign-in. Run with tools/test.sh; nothing here touches a network.

sub main()
    m.failed = 0
    m.passed = 0
    here = {id: "srv_home", name: "Home", apiMajor: 1}

    fresh("srv_home")
    renew()
    check("a renewal first asks the address who it is, and sends no token", m.sent.count() = 1 and m.sent[0].url = "http://home.example/v1/system" and not tokenSent())
    reply(0, 200, here)
    check("the same server: the token is sent to it", m.sent.count() = 2 and m.sent[1].url = "http://home.example/v1/auth/refresh" and tokenSent())

    fresh("srv_home")
    renew()
    reply(0, 200, {id: "srv_other", name: "Someone else", apiMajor: 1})
    check("another server: no token sent", not tokenSent() and m.top.serverChanged = "other")
    check("and the sign-in is kept", m.store.refreshtoken = "kept-token" and m.top.signInEnded <> true)
    check("and a waiting request gets the reason", m.held.result <> invalid and m.held.result.error.code = "server_changed")

    for each odd in [[200, {name: "Home", apiMajor: 1}], [200, invalid], [404, invalid], [200, {id: "srv_home"}]]
        fresh("srv_home")
        renew()
        reply(0, odd[0], odd[1])
        check("an answer that is not Portico's (" + odd[0].toStr() + "): no token sent", not tokenSent() and m.top.serverChanged = "notPortico")
    end for

    fresh("srv_home")
    renew()
    reply(0, 0, invalid)
    check("no answer: no token sent, and the address is not called another server", not tokenSent() and m.top.serverChanged = "" and m.held.result.error.code = "unreachable")

    fresh("")
    renew()
    check("a sign-in kept with no server id: signed out, nothing sent", m.sent.count() = 0 and m.top.signInEnded = true)

    ? "PASSED "; m.passed; " FAILED "; m.failed
end sub

' A task with a kept sign-in and one request waiting for the renewal.
sub fresh(serverId as string)
    m.sent = []
    m.store = {refreshtoken: "kept-token"}
    m.pending = {}
    m.held = {result: invalid}
    m.waiting = [{method: "GET", path: "/v1/me", reply: m.held, public: false}]
    m.renewing = false
    m.clock = {totalMilliseconds: function() as integer : return 0 : end function}
    m.top = {serverChanged: "", signInEnded: false, session: {origin: "http://home.example", serverId: serverId, refreshToken: "kept-token", installationId: "i"}}
end sub

' The network's answer to the n-th request.
sub reply(n as integer, status as integer, data as dynamic)
    body = ""
    if data <> invalid then body = formatJson(data)
    finish({id: n, status: status, body: body, getSourceIdentity: function() as integer : return m.id : end function,
        getResponseCode: function() as integer : return m.status : end function, getString: function() as string : return m.body : end function})
end sub

function tokenSent() as boolean
    for each request in m.sent
        if instr(1, request.body, "kept-token") > 0 then return true
    end for
    return false
end function

sub check(name as string, ok as boolean)
    if ok
        m.passed = m.passed + 1
    else
        m.failed = m.failed + 1
        ? "FAIL "; name
    end if
end sub

' --- stand-ins: the network (components/tasks/Transfer.brs), the store, a request's reply -------------

function newTransfer(url as string) as object
    request = {url: url, body: "", n: m.sent.count(), headers: {},
        addHeader: function(k as string, v as string) as boolean : m.headers[k] = v : return true : end function,
        asyncGetToString: function() as boolean : return true : end function,
        asyncPostFromString: function(body as string) as boolean : m.body = body : return true : end function,
        getIdentity: function() as integer : return m.n : end function}
    m.sent.push(request)
    return request
end function

function storeRead(key as string) as string
    if m.store[lcase(key)] = invalid then return ""
    return m.store[lcase(key)]
end function

sub storeWrite(values as object)
    for each key in values
        m.store[lcase(key)] = values[key]
    end for
end sub

' The app's log (source/AppLog.brs): a failed answer is written to it; here the line goes nowhere.
sub logApp(level as string, area as string, message as string)
end sub
