package textfield

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInsertAndText(t *testing.T) {
	var f Field
	f.Insert("abc")
	assert.Equal(t, "abc", f.Text())
	assert.Equal(t, 3, f.Cursor())

	f.Home()
	f.Insert("x")
	assert.Equal(t, "xabc", f.Text())
	assert.Equal(t, 1, f.Cursor())

	f.End()
	f.Insert("!")
	assert.Equal(t, "xabc!", f.Text())
}

func TestInsertNormalizesNewlines(t *testing.T) {
	var f Field
	f.Insert("a\r\nb\nc\rd")
	assert.Equal(t, "a  b c d", f.Text())
}

func TestDeleteBefore(t *testing.T) {
	var f Field
	assert.False(t, f.DeleteBefore(), "empty field deletes nothing")

	f.SetText("ab")
	assert.True(t, f.DeleteBefore())
	assert.Equal(t, "a", f.Text())
	assert.Equal(t, 1, f.Cursor())
}

func TestDeleteAt(t *testing.T) {
	var f Field
	assert.False(t, f.DeleteAt(), "caret at end deletes nothing")

	f.SetText("ab")
	f.Home()
	assert.True(t, f.DeleteAt())
	assert.Equal(t, "b", f.Text())
	assert.Equal(t, 0, f.Cursor())
}

func TestMoveClamps(t *testing.T) {
	var f Field
	f.SetText("ab")
	f.Move(-10)
	assert.Equal(t, 0, f.Cursor())
	f.Move(10)
	assert.Equal(t, 2, f.Cursor())
	f.Move(-1)
	assert.Equal(t, 1, f.Cursor())
}

func TestKillBeforeAndAfter(t *testing.T) {
	var f Field
	f.SetText("hello")
	f.Move(-3) // caret between "he" and "llo"

	f.KillAfter()
	assert.Equal(t, "he", f.Text())
	assert.Equal(t, 2, f.Cursor())

	f.SetText("hello")
	f.Home()
	f.Move(2)
	f.KillBefore()
	assert.Equal(t, "llo", f.Text())
	assert.Equal(t, 0, f.Cursor())
}

func TestPeekBefore(t *testing.T) {
	var f Field
	_, ok := f.PeekBefore()
	assert.False(t, ok, "empty field has no rune before the caret")

	f.SetText("ab")
	ch, ok := f.PeekBefore()
	assert.True(t, ok, "SetText parks the caret at the end")
	assert.Equal(t, 'b', ch)

	f.Home()
	_, ok = f.PeekBefore()
	assert.False(t, ok, "caret at start has nothing before it")

	f.Move(1)
	ch, ok = f.PeekBefore()
	assert.True(t, ok)
	assert.Equal(t, 'a', ch)
}

func TestSplitAndSetText(t *testing.T) {
	var f Field
	f.SetText("hello")
	f.Move(-2)
	before, after := f.Split()
	assert.Equal(t, "hel", before)
	assert.Equal(t, "lo", after)

	f.SetText("")
	assert.Empty(t, f.Text())
	assert.Equal(t, 0, f.Cursor())
}

func TestCopiesStayIndependent(t *testing.T) {
	var f Field
	f.SetText("abc")
	snapshot := f // what the palette page stack stores

	f.Home()
	f.Insert("x")
	assert.Equal(t, "xabc", f.Text())
	assert.Equal(t, "abc", snapshot.Text(), "shallow copy must not see later edits")
}
