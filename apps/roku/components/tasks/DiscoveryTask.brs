sub init()
    m.top.functionName = "listen"
end sub

' The question to the mDNS group (224.0.0.251, port 5353) at 0, 1 and 3 seconds, and every answer that
' arrives for 10 seconds; each new server is passed on at once, so a row appears as soon as it answers.
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
    asks = [0, 1000, 3000]
    found = []
    seen = {}
    clock = createObject("roTimespan")
    buffer = createObject("roByteArray")
    buffer[1499] = 0      ' room for one packet
    while clock.totalMilliseconds() < 10000
        if asks.count() > 0 and clock.totalMilliseconds() >= asks[0]
            socket.send(question, 0, question.count())
            asks.shift()
        end if
        msg = wait(250, port)
        if type(msg) = "roSocketEvent" and socket.isReadable()
            n = socket.receive(buffer, 0, 1500)
            server = invalid
            if n > 0 then server = mdnsServer(buffer, n)
            if server <> invalid and seen[server.id] = invalid
                seen[server.id] = true
                found.push(server)
                m.top.found = found
            end if
        end if
    end while
    socket.close()
    m.top.done = true
end sub
