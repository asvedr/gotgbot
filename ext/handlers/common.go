package handlers

import (
	"github.com/asvedr/gotgbot/v2"
	"github.com/asvedr/gotgbot/v2/ext"
)

type Response func(b *gotgbot.Bot, ctx *ext.Context) error
