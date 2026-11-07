package read

// An admitted compact AVL set supports exact duplicate reduction and bounded
// rank windows without a final whole-set sort. Authored keys are raw ordinals;
// automatic keys are item IDs. Only IDs/anchors/counts survive a quantum.
type rsCollectionNode struct {
	key, id                   int64
	left, right, size, height int32
}

func rsNodeHeight(a []rsCollectionNode, n int32) int32 {
	if n == 0 {
		return 0
	}
	return a[n-1].height
}
func rsNodeSize(a []rsCollectionNode, n int32) int32 {
	if n == 0 {
		return 0
	}
	return a[n-1].size
}
func rsNodeFix(a []rsCollectionNode, n int32) {
	v := &a[n-1]
	v.height = 1 + max(rsNodeHeight(a, v.left), rsNodeHeight(a, v.right))
	v.size = 1 + rsNodeSize(a, v.left) + rsNodeSize(a, v.right)
}
func rsNodeRotate(a []rsCollectionNode, n int32, right bool) int32 {
	v := &a[n-1]
	var x int32
	if right {
		x = v.left
		v.left = a[x-1].right
		a[x-1].right = n
	} else {
		x = v.right
		v.right = a[x-1].left
		a[x-1].left = n
	}
	rsNodeFix(a, n)
	rsNodeFix(a, x)
	return x
}
func rsNodeInsert(a *[]rsCollectionNode, n int32, key, id int64) int32 {
	if n == 0 {
		*a = append(*a, rsCollectionNode{key: key, id: id, size: 1, height: 1})
		return int32(len(*a))
	}
	v := &(*a)[n-1]
	if key == v.key {
		return n
	}
	if key < v.key {
		v.left = rsNodeInsert(a, v.left, key, id)
	} else {
		v.right = rsNodeInsert(a, v.right, key, id)
	}
	rsNodeFix(*a, n)
	balance := rsNodeHeight(*a, v.left) - rsNodeHeight(*a, v.right)
	if balance > 1 {
		if key > (*a)[v.left-1].key {
			v.left = rsNodeRotate(*a, v.left, false)
		}
		return rsNodeRotate(*a, n, true)
	}
	if balance < -1 {
		if key < (*a)[v.right-1].key {
			v.right = rsNodeRotate(*a, v.right, true)
		}
		return rsNodeRotate(*a, n, false)
	}
	return n
}
func rsNodeRank(a []rsCollectionNode, n int32, rank int) int64 {
	for n != 0 {
		v := a[n-1]
		left := int(rsNodeSize(a, v.left))
		if rank == left {
			return v.id
		}
		if rank < left {
			n = v.left
		} else {
			rank -= left + 1
			n = v.right
		}
	}
	return 0
}
