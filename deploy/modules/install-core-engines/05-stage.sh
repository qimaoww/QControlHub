
# A gzip stream carries no executable mode. Prepare the staged binary before
# version verification; installation only starts after every core is verified.
qch_stage_gzip_binary() {
  qch_gzip_archive=$1
  qch_gzip_binary=$2
  shift 2
  gunzip -c "$qch_gzip_archive" > "$qch_gzip_binary" || return 1
  chmod 0755 "$qch_gzip_binary" || return 1
  "$qch_gzip_binary" "$@"
}
