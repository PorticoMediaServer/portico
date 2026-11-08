package writer

// The full original must be checked before constructing member views. A view
// reuses frozen backing storage and is never submitted or saved as a command.
func validateInventoryFrozenBatch(c *inventoryAcceptCommand) error {
	if c == nil || !c.valid || len(c.request.files) < 1 || len(c.request.files) > maxInventoryAcceptFiles {
		return ErrInvalid
	}
	if err := validateInventoryRequest(c.identity, c.request); err != nil {
		return err
	}
	digest, err := canonicalRequestDigest(InventoryAccept, inventoryAcceptRequestVersion, c.identity, c.request)
	if err != nil || digest != c.digest {
		return ErrConflict
	}
	return nil
}

func inventoryMemberCommand(c *inventoryAcceptCommand, ordinal int) (*inventoryAcceptCommand, error) {
	if c == nil || !c.valid || ordinal < 0 || ordinal >= len(c.request.files) {
		return nil, ErrInvalid
	}
	if len(c.request.files) == 1 {
		return c, nil
	}
	v := *c
	v.request.files = c.request.files[ordinal : ordinal+1]
	v.request.directory.rawNames = 1
	var err error
	v.digest, err = canonicalRequestDigest(InventoryAccept, inventoryAcceptRequestVersion, v.identity, v.request)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func prepareInventoryFreshBatch(c *inventoryAcceptCommand) error {
	if c != nil && len(c.request.files) == 1 {
		_, err := prepareInventoryFreshObservation(c)
		return err
	}
	if err := validateInventoryFrozenBatch(c); err != nil {
		return err
	}
	for i := range c.request.files {
		member, err := inventoryMemberCommand(c, i)
		if err != nil {
			return err
		}
		if _, err = prepareInventoryFreshObservation(member); err != nil {
			return err
		}
	}
	return nil
}
