pandoc index.md ../Requirements/*.md ../Plan/*.md ../Design/*.md \
    -s \
    --toc \
    --css style.css \
    --template eisvogel \
    --pdf-engine=xelatex \
    -o documentation.pdf --pdf-engine=xelatex -V geometry:margin=1.0in

