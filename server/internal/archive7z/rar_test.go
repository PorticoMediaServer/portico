package archive7z

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
)

// The worker decodes the RAR 2.9 LZ stream and a solid continuation itself.
func TestWorkerReadsCompressedAndSolidCBR4(t *testing.T) {
	for name, raw := range map[string][]byte{
		"compressed": readingtest.CBR4Compressed(),
		"solid":      readingtest.CBR4Solid(),
	} {
		t.Run(name, func(t *testing.T) {
			members, err := rarListings(raw)
			if err != nil {
				t.Fatal(err)
			}
			if len(members) != 2 || members[0].Name != "page1.png" || members[1].Name != "page2.png" {
				t.Fatalf("members=%+v", members)
			}
			for _, member := range members {
				if member.Size <= 0 {
					t.Fatalf("%s size=%d", member.Name, member.Size)
				}
			}
			var body bytes.Buffer
			if err = rarPage(bytes.NewReader(raw), &body, "page2.png"); err != nil {
				t.Fatal(err)
			}
			if int64(body.Len()) != members[1].Size || !bytes.HasPrefix(body.Bytes(), []byte("\x89PNG\r\n\x1a\n")) {
				t.Fatalf("page2 %d bytes, want a %d-byte PNG", body.Len(), members[1].Size)
			}
		})
	}
}

// Admission is the same set CB7 serves: image pages and the root ComicInfo.xml,
// with anything oversized or outside the root simply absent.
func TestWorkerRARListAdmitsOnlyInBoundsMembers(t *testing.T) {
	raw := craftRAR4([]craftMember{
		{name: "page.png", size: 4, data: []byte("\x89PNG")},
		{name: "huge.png", size: MaxMember + 1},
		{name: "../evil.png", size: 100},
		{name: "ComicInfo.xml", size: MaxComicInfo + 1},
		{name: "notes.txt", size: 10},
	})
	members, err := rarListings(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].Name != "page.png" || members[0].Size != 4 {
		t.Fatalf("members=%+v", members)
	}
}

// One exact name is one page. Two headers with the same name refuse the archive
// for both list and page rather than serving one of them twice.
func TestWorkerRARRefusesDuplicateMembers(t *testing.T) {
	raw := craftRAR4([]craftMember{
		{name: "page.png", size: 4, data: []byte("\x89PNG")},
		{name: "page.png", size: 4, data: []byte("\x89PNG")},
	})
	if _, err := rarListings(raw); err == nil {
		t.Fatal("list accepted a duplicate member")
	}
	var body bytes.Buffer
	if err := rarPage(bytes.NewReader(raw), &body, "page.png"); err == nil {
		t.Fatal("page accepted a duplicate member")
	}
	if body.Len() != 0 {
		t.Fatalf("page wrote %d bytes before refusing", body.Len())
	}
}

func rarListings(raw []byte) ([]Member, error) {
	var listed bytes.Buffer
	if err := rarList(bytes.NewReader(raw), &listed); err != nil {
		return nil, err
	}
	var reply struct {
		Error   string
		Members []Member
	}
	if err := json.Unmarshal(listed.Bytes(), &reply); err != nil {
		return nil, err
	}
	return reply.Members, nil
}

type craftMember struct {
	name string
	size uint32
	data []byte
}

// craftRAR4 builds a stored RAR4 archive whose headers can declare a size they
// do not carry, so only admission is under test.
func craftRAR4(members []craftMember) []byte {
	var b bytes.Buffer
	b.WriteString("Rar!\x1a\x07\x00")
	b.Write(craftRAR4Block(0x73, 0, make([]byte, 6), nil))
	for _, member := range members {
		fields := binary.LittleEndian.AppendUint32(nil, uint32(len(member.data)))
		fields = binary.LittleEndian.AppendUint32(fields, member.size)
		fields = append(fields, 3) // host OS: Unix
		fields = binary.LittleEndian.AppendUint32(fields, crc32.ChecksumIEEE(member.data))
		fields = binary.LittleEndian.AppendUint32(fields, 0x5a210000) // DOS time
		fields = append(fields, 20, 0x30)
		fields = binary.LittleEndian.AppendUint16(fields, uint16(len(member.name)))
		fields = binary.LittleEndian.AppendUint32(fields, 0o100644<<16)
		fields = append(fields, member.name...)
		b.Write(craftRAR4Block(0x74, 0x8000, fields, member.data))
	}
	b.Write(craftRAR4Block(0x7b, 0x4000, nil, nil))
	return b.Bytes()
}

func craftRAR4Block(kind byte, flags uint16, fields, data []byte) []byte {
	header := []byte{kind}
	header = binary.LittleEndian.AppendUint16(header, flags)
	header = binary.LittleEndian.AppendUint16(header, uint16(7+len(fields)))
	header = append(header, fields...)
	out := binary.LittleEndian.AppendUint16(nil, uint16(crc32.ChecksumIEEE(header)))
	out = append(out, header...)
	return append(out, data...)
}
