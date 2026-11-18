' One request to a server, as Roku's roUrlTransfer: JSON both ways, the system's certificates for
' HTTPS. Its own file so the task's tests can stand in for the network (tests/serverid.brs).
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
