package writer

import "fmt"

// CommandID is a compiled purpose, never a caller-provided priority or SQL key.
// A descriptor is metadata only: it neither authorizes nor qualifies execution.
type CommandID uint16

type Durability uint8

const (
	Normal Durability = iota + 1
	Full
)

type Descriptor struct {
	ID         CommandID
	Name       string
	Durability Durability
}

// Domain declarations are private, compiled data, copied once into this table.
// There is no registration API and no runtime append/mutation path.
type commandRegistration struct {
	owner       string
	descriptors []Descriptor
}

var closedCommandDescriptors = mustBuildCommandDescriptors(
	phase1aCommands, iaCommands, ipCommands, rbCommands, rsCommands,
	usCommands, uoCommands, mpCommands, mgCommands,
)

func commandIDRange(owner string) (CommandID, CommandID, bool) {
	switch owner {
	case "phase1a":
		return 1, 99, true
	case "IA":
		return 100, 199, true
	case "IP":
		return 200, 299, true
	case "RB":
		return 300, 399, true
	case "RS":
		return 400, 499, true
	case "US":
		return 500, 599, true
	case "UO":
		return 600, 699, true
	case "MP":
		return 700, 799, true
	case "MG":
		return 800, 899, true
	default:
		return 0, 0, false
	}
}

// This pure builder validates compiled declarations; it never changes the
// package table. Only the package initializer above installs its result.
func buildCommandDescriptors(registrations ...commandRegistration) (map[CommandID]Descriptor, error) {
	table := make(map[CommandID]Descriptor)
	names := make(map[string]bool)
	for _, registration := range registrations {
		first, last, known := commandIDRange(registration.owner)
		if !known {
			return nil, fmt.Errorf("writer command registry: unknown owner %q", registration.owner)
		}
		for _, d := range registration.descriptors {
			if d.ID < first || d.ID > last {
				return nil, fmt.Errorf("writer command registry: ID %d outside %s range %d–%d", d.ID, registration.owner, first, last)
			}
			if d.Name == "" {
				return nil, fmt.Errorf("writer command registry: empty name for ID %d", d.ID)
			}
			if d.Durability != Normal && d.Durability != Full {
				return nil, fmt.Errorf("writer command registry: unknown durability for ID %d", d.ID)
			}
			if _, duplicate := table[d.ID]; duplicate {
				return nil, fmt.Errorf("writer command registry: duplicate ID %d", d.ID)
			}
			if names[d.Name] {
				return nil, fmt.Errorf("writer command registry: duplicate name %q", d.Name)
			}
			table[d.ID], names[d.Name] = d, true
		}
	}
	return table, nil
}

func mustBuildCommandDescriptors(registrations ...commandRegistration) map[CommandID]Descriptor {
	table, err := buildCommandDescriptors(registrations...)
	if err != nil {
		panic(err)
	}
	return table
}

// Describe is a closed registry. Qualification budgets are recipe/profile-specific
// and must be supplied by the private executor after measurement; they are not
// inferred from a purpose name. No SQL implementation is registered here yet.
func Describe(id CommandID) (Descriptor, error) {
	d, ok := closedCommandDescriptors[id]
	if !ok {
		return Descriptor{}, invalid("command")
	}
	return d, nil
}
