sub init()
    m.top.functionName = "listen"
end sub

' One question to the mDNS group (224.0.0.251, port 5353), then every answer that arrives until the
' time is up; a server also announces itself every 30 seconds, which is caught the same way.
sub listen()
    port = createObject("roMessagePort")
    socket = createObject("roDatagramSocket")
    socket.setMessagePort(port)
    socket.setReuseAddr(true)
    here = createObject("roSocketAddress")
    here.setPort(5353)
    socket.setAddress(here)
    group = createObject("roSocketAddress")
    group.setHostName("224.0.0.251")
    group.setPort(5353)
    socket.joinGroup(group)
    socket.setSendToAddress(group)
    socket.notifyReadable(true)
    question = mdnsQuery()
    socket.send(question, 0, question.count())
    found = []
    seen = {}
    clock = createObject("roTimespan")
    buffer = createObject("roByteArray")
    buffer[1499] = 0      ' room for one packet
    while clock.totalMilliseconds() < 3500
        msg = wait(250, port)
        if type(msg) = "roSocketEvent" and socket.isReadable()
            n = socket.receive(buffer, 0, 1500)
            server = invalid
            if n > 0 then server = mdnsServer(buffer, n)
            if server <> invalid and seen[server.id] = invalid
                seen[server.id] = true
                found.push(server)
            end if
        end if
    end while
    socket.close()
    m.top.found = found
end sub
