package catalog

import (
	"context"
	"encoding/json"

	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// Small display conversion and Queue have different support boundaries.
// ForYou <=200 is an exact prepared conversion, not a general Queue iterator.
func localSearchConversionOrderSupported(node *BrowseNode, sorts []BrowseSortSelection) bool {
	if len(sorts) != 1 {
		return false
	}
	if LocalSearchQueueSupported(node, sorts) {
		return true
	}
	return browseQueryReplayable(node, sorts) && sorts[0].Field == "forYou" && sorts[0].Direction == "desc"
}

func (c *selectionPreparationComputation) prepareSmallOrder(ctx context.Context, budget *readprep.Budget, witness SelectionBatchWitness) (bool, error) {
	p := c.state.plan
	var sorts []BrowseSortSelection
	if err := json.Unmarshal(p.sorts, &sorts); err != nil {
		return false, err
	}
	if c.smallCount > BrowseSelectionConversionLimit || len(sorts) != 1 || sorts[0].Field != "groupOrder" && sorts[0].Field != "forYou" {
		return false, nil
	}
	if c.order == nil {
		var err error
		if sorts[0].Field == "groupOrder" {
			var query *BrowseNode
			if err = json.Unmarshal(p.query, &query); err != nil {
				return false, err
			}
			c.order, err = NewSmallGroupOrder(c.smallIDs[:c.smallCount], groupOrderTarget(query), sorts[0].Direction, p.witness)
		} else {
			var viewer Viewer
			if err = json.Unmarshal(p.policy, &viewer); err != nil {
				return false, err
			}
			c.order, err = NewSmallForYouOrder(c.smallIDs[:c.smallCount], viewer.Profile, p.library, p.witness, c.instant)
			if err == nil {
				err = c.order.bindForYouMemoScope(c.memoScope)
			}
		}
		if err != nil {
			return false, err
		}
	}
	if !c.order.Complete() {
		next, _, err := c.service.StepSmallSelectionOrder(ctx, c.order, witness, SmallSelectionOrderLimits{ReserveTransient: func(n int64) error { return budget.Resize(c.AccountedBytes() + n + 65536) }})
		if err != nil {
			return false, selectionPreparationError(err)
		}
		c.order = next
		if !next.Complete() {
			return true, nil
		}
	}
	ordered, err := c.order.IDs()
	if err != nil {
		return false, err
	}
	copy(c.smallIDs[:], ordered)
	return false, nil
}

func (c *selectionReplayComputation) prepareGroupKeys(ctx context.Context, budget *readprep.Budget, witness SelectionBatchWitness) (bool, error) {
	var sorts []BrowseSortSelection
	if err := json.Unmarshal(c.state.plan.sorts, &sorts); err != nil {
		return false, err
	}
	if len(sorts) != 1 || sorts[0].Field != "groupOrder" {
		return false, nil
	}
	if c.order == nil {
		var query *BrowseNode
		if err := json.Unmarshal(c.state.plan.query, &query); err != nil {
			return false, err
		}
		order, err := NewSmallGroupOrder(c.ids[:c.count], groupOrderTarget(query), sorts[0].Direction, c.state.plan.witness)
		if err != nil {
			return false, err
		}
		c.order = order
	}
	if !c.order.Complete() {
		next, _, err := c.service.StepSmallSelectionOrder(ctx, c.order, witness, SmallSelectionOrderLimits{ReserveTransient: func(n int64) error { return budget.Resize(c.AccountedBytes() + n + 65536) }})
		if err != nil {
			return false, selectionPreparationError(err)
		}
		c.order = next
		if !next.Complete() {
			return true, nil
		}
	}
	var err error
	c.sortKeys, err = c.order.GroupCommandKeys(func(n int64) error { return budget.Resize(c.AccountedBytes() + n) })
	return false, err
}
