# Opprette skjemaer for datainnsamling

Data samles inn i instrument (skjema) ved hendelser (besøk). Legg inn hendelsene på **Oppsett**; instrumentene og feltene lages på **Utforming**. Du må ha rollen prosjektadmin.

1. Åpne prosjektet og velg **Oppsett**. Under **Hendelser** skriver du navnet på hendelsen og, om besøket har ett, tidspunktet i dager, og velger så **Legg til hendelse**.
2. Velg **Utforming** i panelet til venstre. Under instrumentlisten skriver du et navn på det nye instrumentet og velger **Legg til instrument**.
3. Tilbake på **Oppsett**, i **Instrument × hendelser**, krysser du av for hvilke hendelser hvert instrument skal fylles ut ved, og velger **Bruk**. Et instrument uten en avkrysset hendelse samler ikke inn data.
4. Åpne et instrument på **Utforming**. Det første feltet i det første instrumentet er registreringsidentifikatoren (for eksempel `record_id`).
5. Velg **Legg til felt**. Gi feltet et navn (små bokstaver, siffer og _), en etikett og en type. For radioknapper, nedtrekkslister og avkryssingsbokser skriver du én kode og én etikett per valg. Validering, **Påkrevd**, forgreningslogikk og de andre innstillingene er valgfrie. Lagre feltet.
6. Feltlisten viser feltene til instrumentet i skjemaorden. Bruk pilene til å endre rekkefølgen, **Rediger** til å endre, **Flytt** til å flytte et felt til et annet instrument og **Fjern** til å slette det.

## Tips

Instrument kan også importeres fra REDCap-instrument-ZIP-filer eller CSV-filer med datadictionary på siden **Utforming**.
