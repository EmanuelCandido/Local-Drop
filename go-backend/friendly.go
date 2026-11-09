package main

import (
	"errors"
	"io/fs"
	"strings"
)

// friendlyError traduz uma falha em uma frase simples e no que a pessoa pode
// fazer. O texto técnico continua só no log.
type friendlyError struct {
	Title string
	Hint  string
}

var friendlyByCode = map[string]friendlyError{
	"INSUFFICIENT_STORAGE":            {"O computador está sem espaço livre.", "Libere espaço no disco ou escolha outra pasta em Ajustes."},
	"STORAGE_UNAVAILABLE":             {"Não deu para usar a pasta de recebimento.", "Escolha outra pasta em Ajustes."},
	"DOWNLOAD_DIRECTORY_INVALID":      {"Essa pasta não pode ser usada.", "Escolha uma pasta do computador, como Downloads ou Documentos."},
	"DOWNLOAD_DIRECTORY_NOT_WRITABLE": {"O Windows não deixa salvar nessa pasta.", "Escolha outra pasta, como Downloads ou Documentos."},
	"DOWNLOAD_DIRECTORY_IN_USE":       {"A pasta não pode mudar agora.", "Espere o recebimento terminar e tente de novo."},
	"TRANSFER_INVALID_STATE":          {"Essa ação não vale mais para este envio.", "A lista já foi atualizada. Confira o estado atual."},
	"TRANSFER_NOT_FOUND":              {"Este envio não existe mais.", "Ele pode ter sido cancelado no outro aparelho."},
	"TRANSFER_SOURCE_CHANGED":         {"O arquivo mudou depois de ser oferecido.", "Envie o arquivo de novo."},
	"TRANSFER_FILENAME_INVALID":       {"O nome de um arquivo tem caracteres que o Windows não aceita.", "Renomeie o arquivo e tente de novo."},
	"TRANSFER_FILE_INVALID":           {"Um dos itens não é um arquivo comum.", "Escolha só arquivos (atalhos e itens especiais não podem ser enviados)."},
	"TRANSFER_FILES_REQUIRED":         {"Escolha entre 1 e 1000 arquivos por vez.", "Divida o envio em partes menores."},
	"TRANSFER_RECEIVER_NOT_TRUSTED":   {"Esse aparelho não está mais conectado.", "Conecte o celular de novo pelo QR Code."},
	"TRANSFER_RATE_LIMITED":           {"Muitos envios de uma vez.", "Espere alguns segundos e tente de novo."},
	"TRANSFER_DOWNLOAD_EXPIRED":       {"Esse arquivo não está mais disponível.", "Envie de novo."},
	"PAIRING_EXPIRED":                 {"O pedido de conexão venceu.", "No celular, toque em Conectar de novo."},
	"PAIRING_NOT_PENDING":             {"Esse pedido já foi respondido.", "Nada a fazer: a lista foi atualizada."},
	"PAIRING_NOT_FOUND":               {"Esse pedido de conexão não existe mais.", "No celular, toque em Conectar de novo."},
	"PAIRING_CODE_INVALID":            {"O código não confere.", "Peça para o celular tentar de novo."},
	"DEVICE_NOT_FOUND":                {"Esse aparelho não está mais na lista.", "Nada a fazer: a lista foi atualizada."},
	"DEVICE_NAME_INVALID":             {"Esse nome não pode ser usado.", "Use de 1 a 60 letras ou números."},
	"HOST_CANNOT_BE_REVOKED":          {"Este computador não pode ser removido.", "Para parar de usar o LocalDrop, feche-o pela bandeja."},
}

func explainError(err error) friendlyError {
	var api *apiProblem
	if errors.As(err, &api) {
		if known, ok := friendlyByCode[api.Code]; ok {
			return known
		}
		if api.Message != "" {
			return friendlyError{Title: api.Message, Hint: "Tente de novo. Se continuar, feche e abra o LocalDrop."}
		}
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return friendlyError{"O arquivo não foi encontrado.", "Ele pode ter sido movido, renomeado ou apagado."}
	case errors.Is(err, fs.ErrPermission):
		return friendlyError{"O Windows não deixou usar esse arquivo.", "Feche o programa que está com ele aberto e tente de novo."}
	case err != nil && strings.Contains(strings.ToLower(err.Error()), "1000 arquivos"):
		return friendlyError{"A pasta tem mais de 1000 arquivos.", "Envie uma pasta menor ou compacte os arquivos antes."}
	}
	return friendlyError{"Algo deu errado.", "Tente de novo. Se continuar, feche e abra o LocalDrop."}
}
