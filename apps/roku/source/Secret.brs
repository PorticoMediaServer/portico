' A Portico Account TV code's secret (AccountPages): made on the TV, kept on the TV; Portico Services
' is told only its digest, so only this TV can collect the sign-in its code is approved for.

' 32 random bytes as base64url (43 characters), from two random UUIDs' hex digits.
function secretText() as string
    info = createObject("roDeviceInfo")
    hex = (info.getRandomUUID() + info.getRandomUUID()).replace("-", "")
    bytes = createObject("roByteArray")
    bytes.fromHexString(hex)
    return urlSafe(bytes.toBase64String())
end function

' The SHA-256 digest of a text, as base64url: what Services keeps of the secret.
function digestText(text as string) as string
    bytes = createObject("roByteArray")
    bytes.fromAsciiString(text)
    digest = createObject("roEVPDigest")
    digest.setup("sha256")
    out = createObject("roByteArray")
    out.fromHexString(digest.process(bytes))
    return urlSafe(out.toBase64String())
end function

function urlSafe(base64 as string) as string
    return base64.replace("+", "-").replace("/", "_").replace("=", "")
end function
