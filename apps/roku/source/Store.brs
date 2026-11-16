' What the app remembers between runs (Roku's registry): the server, the sign-in and the device's own id.
function storeRead(key as string) as string
    sec = createObject("roRegistrySection", "portico")
    key = lcase(key)   ' an object's keys arrive in lower case, so every key is kept that way
    if sec.exists(key) then return sec.read(key)
    return ""
end function

sub storeWrite(values as object)
    sec = createObject("roRegistrySection", "portico")
    for each key in values
        if values[key] = invalid or values[key] = "" then
            sec.delete(lcase(key))
        else
            sec.write(lcase(key), values[key])
        end if
    end for
    sec.flush()
end sub

' The id this installation made for itself (the server asks for 32 to 128 letters, digits, _ or -).
function installationId() as string
    id = storeRead("installationId")
    if id = "" then
        id = "roku-" + createObject("roDeviceInfo").getRandomUUID()
        storeWrite({installationId: id})
    end if
    return id
end function
