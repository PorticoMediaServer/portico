' What a person types for a server is rarely a full address. This is the same rule as every other
' Portico app (packages/client-core server-address.ts): the short list of addresses worth trying,
' most likely first. A typed scheme is used as typed; a typed port is kept; with no port the server's
' own port (32500) is tried as well; a home-looking name or address is tried over plain HTTP first.
' Returns an empty list for something that cannot be an address.
function addressCandidates(typed as string) as object
    value = typed.trim()
    while right(value, 1) = "/"
        value = left(value, len(value) - 1)
    end while
    if value = "" or len(value) > 300 then return []
    for each bad in [" ", "\", "@", chr(9)]
        if instr(1, value, bad) > 0 then return []
    end for
    low = lcase(value)
    if left(low, 7) = "http://" or left(low, 8) = "https://" then return [value]
    if instr(1, value, "://") > 0 then return []
    authority = value.split("/")[0]
    host = lcase(authority)
    port = ""
    if left(host, 1) = "["
        close = instr(1, host, "]")
        if close = 0 then return []
        if len(host) > close
            if mid(host, close + 1, 1) <> ":" then return []
            port = mid(host, close + 2)
        end if
        host = left(host, close)
    else
        parts = host.split(":")
        if parts.count() > 2 then return []   ' a bare IPv6 address is typed in brackets on a TV
        if parts.count() = 2
            host = parts[0]
            port = parts[1]
        end if
        if host = "" or createObject("roRegex", "^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$", "").isMatch(host) = false then return []
    end if
    if port <> ""
        if not createObject("roRegex", "^\d{1,5}$", "").isMatch(port) then return []
        if port.toInt() < 1 or port.toInt() > 65535 then return []
        if homeLooking(host) then return ["http://" + host + ":" + port, "https://" + host + ":" + port]
        return ["https://" + host + ":" + port, "http://" + host + ":" + port]
    end if
    if homeLooking(host) then return ["http://" + host + ":32500", "https://" + host + ":32500", "https://" + host, "http://" + host]
    return ["https://" + host, "https://" + host + ":32500", "http://" + host + ":32500"]
end function

' A host that looks like it lives on the home network: a private or link-local address, a bare
' name, or a home-network suffix.
function homeLooking(host as string) as boolean
    if left(host, 1) = "[" then return left(host, 3) = "[fe" or left(host, 3) = "[fc" or left(host, 3) = "[fd" or host = "[::1]"
    if createObject("roRegex", "^\d{1,3}(\.\d{1,3}){3}$", "").isMatch(host)
        p = host.split(".")
        a = p[0].toInt()
        b = p[1].toInt()
        return a = 10 or a = 127 or (a = 192 and b = 168) or (a = 172 and b >= 16 and b <= 31) or (a = 169 and b = 254) or (a = 100 and b >= 64 and b <= 127)
    end if
    if instr(1, host, ".") = 0 then return true
    for each suffix in [".local", ".lan", ".home", ".internal", ".home.arpa", ".localdomain"]
        if right(host, len(suffix)) = suffix then return true
    end for
    return false
end function

' An address as a person reads it: without its scheme.
function addressShown(origin as string) as string
    return origin.replace("https://", "").replace("http://", "")
end function
