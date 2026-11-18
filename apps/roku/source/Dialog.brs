' Roku's own keyboard dialog, for what Portico's keyboard cannot type well: an address, a name or a
' password. The scene's palette gives it Portico's colours. The caller observes buttonSelected (0 is
' OK) and wasClosed, and shows it with getScene().dialog.
function keyboardDialog(title as string, message as string, text as string, secret as boolean) as object
    dialog = createObject("roSGNode", "StandardKeyboardDialog")
    dialog.title = title
    dialog.message = [message]
    dialog.buttons = ["OK", "Cancel"]
    dialog.text = text
    if secret then dialog.textEditBox.secureMode = true
    return dialog
end function
