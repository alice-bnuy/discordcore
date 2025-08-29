#!/bin/bash

# Verifica se a branch de testes está atualizada
# Sempre fazer checkout pra branch de testes
git checkout alice-main
git pull origin alice-main

# Parse arguments
while getopts "m:" opt; do
  case $opt in
    m) COMMIT_MESSAGE="$OPTARG" ;;
    *) echo "Uso: $0 -m \"mensagem de commit\" vX.X.X"; exit 1 ;;
  esac
done
shift $((OPTIND -1))

# Verifica se a mensagem de commit foi fornecida
if [ -z "$COMMIT_MESSAGE" ]; then
  echo "Erro: Você precisa fornecer uma mensagem de commit com a flag -m. Exemplo: ./release.sh -m \"mensagem\" v2.5.0"
  exit 1
fi

# Verifica se a versão foi fornecida
VERSION=$1
if [ -z "$VERSION" ]; then
  echo "Erro: Você precisa fornecer uma versão. Exemplo: ./release.sh -m \"mensagem\" v2.5.0"
  exit 1
fi

# Adicionar arquivos, mensagem de commit e enviar para branch origin
git add .
git commit -m "$COMMIT_MESSAGE"
git push origin alice-main

# Faz merge com a branch principal
git checkout main
git fetch
git merge alice-main

# Envia as alterações para a branch principal
git push origin main

# Cria uma nova tag
git tag -m "$COMMIT_MESSAGE" $VERSION
git push origin $VERSION

echo "Release $VERSION criada e enviada com sucesso!"
