

cat /var/log/apache2/access.log | docker run --rm -i -e LANG=$LANG allinurl/goaccess -a -o html --log-format COMBINED - > report.html