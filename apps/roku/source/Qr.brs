' A QR code for a sign-in address, made on the television (Roku has no QR drawing of its own).
' Byte mode, error correction M, versions 1 to 10 (up to 213 bytes), mask 0.
' qrMatrix(text) returns the rows as strings of "1" (dark) and "0", or invalid if the text is too long.

function qrXor(a as integer, b as integer) as integer
    return (a or b) - (a and b)
end function

function qrMatrix(text as string) as dynamic
    bytes = createObject("roByteArray")
    bytes.fromAsciiString(text)
    ' per version: [data codewords, error codewords per block, blocks of the short size, blocks one longer]
    spec = [invalid, [16, 10, 1, 0], [28, 16, 1, 0], [44, 26, 1, 0], [64, 18, 2, 0], [86, 24, 2, 0], [108, 16, 4, 0], [124, 18, 4, 0], [154, 22, 2, 2], [182, 22, 3, 2], [216, 26, 4, 1]]
    version = 0
    for v = 1 to 10
        lengthBits = 8
        if v >= 10 then lengthBits = 16
        if 4 + lengthBits + bytes.count() * 8 <= spec[v][0] * 8
            version = v
            exit for
        end if
    end for
    if version = 0 then return invalid
    s = spec[version]

    ' the data: mode, length, bytes, terminator, padding
    bits = []
    qrPush(bits, 4, 4)
    if version >= 10 then qrPush(bits, bytes.count(), 16) else qrPush(bits, bytes.count(), 8)
    for each b in bytes
        qrPush(bits, b, 8)
    end for
    capacity = s[0] * 8
    for i = 1 to 4
        if bits.count() < capacity then bits.push(0)
    end for
    while bits.count() mod 8 <> 0
        bits.push(0)
    end while
    data = []
    for i = 0 to bits.count() - 1 step 8
        value = 0
        for j = 0 to 7
            value = value * 2 + bits[i + j]
        end for
        data.push(value)
    end for
    pad = [236, 17]
    n = 0
    while data.count() < s[0]
        data.push(pad[n mod 2])
        n = n + 1
    end while

    ' split into blocks, add error correction, interleave
    gf = qrField()
    generator = qrGenerator(gf, s[1])
    blocks = []
    eccs = []
    at = 0
    short = int(s[0] / (s[2] + s[3]))
    for b = 0 to s[2] + s[3] - 1
        size = short
        if b >= s[2] then size = short + 1
        block = []
        for i = 0 to size - 1
            block.push(data[at + i])
        end for
        at = at + size
        blocks.push(block)
        eccs.push(qrRemainder(gf, block, generator))
    end for
    stream = []
    for i = 0 to short
        for each block in blocks
            if i < block.count() then stream.push(block[i])
        end for
    end for
    for i = 0 to s[1] - 1
        for each ecc in eccs
            stream.push(ecc[i])
        end for
    end for

    ' the grid: -1 is free, 0 light, 1 dark; `fixed` marks the function patterns
    size = 17 + version * 4
    grid = []
    fixed = []
    for y = 0 to size - 1
        row = []
        frow = []
        for x = 0 to size - 1
            row.push(0)
            frow.push(false)
        end for
        grid.push(row)
        fixed.push(frow)
    end for
    for each corner in [[0, 0], [size - 7, 0], [0, size - 7]]
        for dy = -1 to 7
            for dx = -1 to 7
                x = corner[0] + dx
                y = corner[1] + dy
                if x >= 0 and x < size and y >= 0 and y < size
                    dark = dx >= 0 and dx <= 6 and dy >= 0 and dy <= 6 and (dx = 0 or dx = 6 or dy = 0 or dy = 6 or (dx >= 2 and dx <= 4 and dy >= 2 and dy <= 4))
                    qrSet(grid, fixed, x, y, dark)
                end if
            end for
        end for
    end for
    centres = [invalid, [], [6, 18], [6, 22], [6, 26], [6, 30], [6, 34], [6, 22, 38], [6, 24, 42], [6, 26, 46], [6, 28, 50]][version]
    ' (by index: on a Roku a list has one place-marker, so one list cannot be walked by two "for each" at once)
    for a = 0 to centres.count() - 1
        for b = 0 to centres.count() - 1
            cy = centres[a]
            cx = centres[b]
            if not fixed[cy][cx]
                for dy = -2 to 2
                    for dx = -2 to 2
                        qrSet(grid, fixed, cx + dx, cy + dy, abs(dx) = 2 or abs(dy) = 2 or (dx = 0 and dy = 0))
                    end for
                end for
            end if
        end for
    end for
    ' the timing lines go on after the alignment squares, which they run through
    for i = 8 to size - 9
        qrSet(grid, fixed, i, 6, i mod 2 = 0)
        qrSet(grid, fixed, 6, i, i mod 2 = 0)
    end for
    ' format (level M, mask 0) and, from version 7, the version: reserve and write
    format = qrBch(0, 5, 1335, 10)                 ' level M is 00, mask 0 is 000; generator 0x537
    format = qrXor(format, 21522)                   ' 0x5412
    for i = 0 to 14
        bit = ((format >> i) and 1) = 1
        ' around the top-left finder
        if i < 6
            qrSet(grid, fixed, 8, i, bit)
        else if i < 8
            qrSet(grid, fixed, 8, i + 1, bit)
        else if i = 8
            qrSet(grid, fixed, 7, 8, bit)
        else
            qrSet(grid, fixed, 14 - i, 8, bit)
        end if
        ' the copy by the other two finders
        if i < 8
            qrSet(grid, fixed, size - 1 - i, 8, bit)
        else
            qrSet(grid, fixed, 8, size - 15 + i, bit)
        end if
    end for
    qrSet(grid, fixed, 8, size - 8, true)
    if version >= 7
        info = qrBch(version, 6, 7973, 12)          ' generator 0x1F25
        for i = 0 to 17
            bit = ((info >> i) and 1) = 1
            a = int(i / 3)
            b = size - 11 + (i mod 3)
            qrSet(grid, fixed, a, b, bit)
            qrSet(grid, fixed, b, a, bit)
        end for
    end if

    ' lay the stream in the zigzag, masked with pattern 0: (x + y) mod 2 = 0
    index = 0
    total = stream.count() * 8
    x = size - 1
    upward = true
    while x > 0
        if x = 6 then x = 5
        for n = 0 to size - 1
            y = n
            if upward then y = size - 1 - n
            for k = 0 to 1
                xx = x - k
                if not fixed[y][xx]
                    bit = 0
                    if index < total then bit = (stream[index >> 3] >> (7 - (index and 7))) and 1
                    index = index + 1
                    if (xx + y) mod 2 = 0 then bit = 1 - bit
                    grid[y][xx] = bit
                end if
            end for
        end for
        upward = not upward
        x = x - 2
    end while

    rows = []
    for each row in grid
        line = ""
        for each cell in row
            line = line + cell.toStr()
        end for
        rows.push(line)
    end for
    return rows
end function

sub qrPush(bits as object, value as integer, count as integer)
    for i = count - 1 to 0 step -1
        bits.push((value >> i) and 1)
    end for
end sub

sub qrSet(grid as object, fixed as object, x as integer, y as integer, dark as boolean)
    if dark then grid[y][x] = 1 else grid[y][x] = 0
    fixed[y][x] = true
end sub

' value followed by its BCH remainder: `bits` data bits, generator of degree `degree`.
function qrBch(value as integer, bits as integer, generator as integer, degree as integer) as integer
    rest = value << degree
    for i = bits - 1 to 0 step -1
        if ((rest >> (i + degree)) and 1) = 1 then rest = qrXor(rest, generator << i)
    end for
    return (value << degree) or rest
end function

' GF(256) with the QR polynomial 0x11D: {exp, log}
function qrField() as object
    e = []
    l = []
    for i = 0 to 255
        l.push(0)
    end for
    x = 1
    for i = 0 to 254
        e.push(x)
        l[x] = i
        x = x * 2
        if x > 255 then x = qrXor(x, 285)
    end for
    for i = 255 to 511
        e.push(e[i - 255])
    end for
    return {exp: e, log: l}
end function

function qrMul(gf as object, a as integer, b as integer) as integer
    if a = 0 or b = 0 then return 0
    return gf.exp[gf.log[a] + gf.log[b]]
end function

' The Reed-Solomon generator polynomial of the given degree, highest power first, without its leading 1.
function qrGenerator(gf as object, degree as integer) as object
    poly = [1]
    for i = 0 to degree - 1
        nextPoly = []
        for j = 0 to poly.count()
            nextPoly.push(0)
        end for
        for j = 0 to poly.count() - 1
            nextPoly[j] = qrXor(nextPoly[j], poly[j])
            nextPoly[j + 1] = qrXor(nextPoly[j + 1], qrMul(gf, poly[j], gf.exp[i]))
        end for
        poly = nextPoly
    end for
    poly.shift()
    return poly
end function

function qrRemainder(gf as object, block as object, generator as object) as object
    degree = generator.count()
    rest = []
    for i = 0 to degree - 1
        rest.push(0)
    end for
    for each value in block
        factor = qrXor(value, rest.shift())
        rest.push(0)
        for i = 0 to degree - 1
            rest[i] = qrXor(rest[i], qrMul(gf, generator[i], factor))
        end for
    end for
    return rest
end function
