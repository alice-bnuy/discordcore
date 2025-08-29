#!/bin/sh

# Busca todas as tags do repositório remoto
git fetch --tags

# Obtém a última versão (última tag ordenada)
latest_version=$(git tag --sort=-v:refname | head -n 1)

# Extrai o prefixo da última versão (exclui o número de patch)
prefix=$(echo "$latest_version" | sed 's/\.[0-9]*$//')

# Lista as tags que começam com o prefixo da última versão
git tag -l "${prefix}.*"