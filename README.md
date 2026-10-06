# Ricomposizione — Progress Tracker

Progetto inizializzato a partire dalla conversazione ChatGPT
[_"Programma palestra 3 giorni"_](https://chatgpt.com/share/6ac499e1-17a8-83eb-89f8-8048abe883e5).

L'obiettivo è la **ricomposizione corporea**: costruire/recuperare massa muscolare
riducendo gradualmente il grasso, con un deficit calorico moderato.

## Cosa contiene

| File | Descrizione |
|---|---|
| [`index.html`](./index.html) | App web (single file) per tracciare peso, calorie e macro. Salva tutto in **LocalStorage**, nessun server. |
| [`data/cibi.csv`](./data/cibi.csv) | Database alimenti d'esempio, esportato dal foglio **“Cibo”** di `~/Documents/dieta/macros.xlsx`. |
| [`docs/scheda-allenamento.md`](./docs/scheda-allenamento.md) | Scheda palestra 3 giorni (2× upper / 2× lower). |
| [`docs/piano-nutrizionale.md`](./docs/piano-nutrizionale.md) | Target calorici e macro (giorni allenamento vs riposo). |

## Profilo di riferimento

- 22 anni, maschio
- 172 cm, 72,7 kg
- Vita sedentaria + 3 allenamenti/settimana
- ~25 min di cardio per sessione (15' prima + 10' dopo)
- Obiettivo: ricomposizione **abbastanza aggressiva**

## Come usare il tracker

Apri `index.html` nel browser (anche da smartphone Android). Puoi servirlo
localmente con:

```bash
# Python
python3 -m http.server 8000

# oppure Node
npx serve .
```

Poi visita <http://localhost:8000>.

### Funzionalità

L'interfaccia è divisa in **tab**:

- **Dashboard** — peso attuale, medie 7 giorni (peso, kcal, P/G/C), nuova registrazione, grafico del peso.
- **Diario** — alimenti consumati per data, con grammi e totali kcal/macro vs target.
- **Cibi** — database alimenti: aggiungi/modifica manualmente, import/export CSV, ricerca e filtro per categoria.
- **Storico** — tutte le registrazioni con peso, kcal, macro e tipo giornata.

In dettaglio:

- 📊 Andamento del peso con grafico
- ⚖️ Peso giornaliero
- 🔥 Calorie introdotte
- 🏋️ Distinzione allenamento / riposo
- 📈 Medie degli ultimi 7 giorni (peso, kcal, proteine, grassi, carboidrati)
- 🗂️ Storico completo con macro
- 💾 Salvataggio automatico in LocalStorage
- 🗑️ Eliminazione singola registrazione o di tutto lo storico
- 📱 Interfaccia responsive per Android
- 🥩 **Database cibi** importabile da CSV (vedi sotto)
- ➕ **Aggiunta/modifica manuale degli alimenti** dall'interfaccia
- 📓 **Diario alimentare**: aggiungi gli alimenti con i grammi e l'app calcola kcal + macro vs target
- 🤖 **Suggerimento greedy**: cosa mangiare nel resto della giornata per completare kcal e macro

> I dati restano **sul dispositivo/browser** e non vengono inviati a nessun server.

## ➕ Aggiungere un alimento a mano

Nel tab **Cibi**, il form in alto permette di creare un alimento con:

| Campo | Note |
|---|---|
| Nome | obbligatorio |
| Proteine / Grassi / Carboidrati | g per 100 g |
| kcal | se lasciato vuoto viene calcolata da `P×4 + C×4 + G×9` |
| Min / Max | porzione consigliata in grammi (opzionale) |
| Categoria | tag separati da virgola, es. `proteina,latticino` |
| Pezzo | grammi per pezzo (opzionale) |

Ogni riga della tabella ha i pulsanti **Aggiungi** (al diario), **Modifica** e **×** (elimina).

## 🥩 Importare un CSV di cibi

Nel database cibi puoi **importare un CSV** con la stessa struttura del secondo
foglio (`Cibo`) di `~/Documents/dieta/macros.xlsx`:

```csv
Food (100gr);Proteine;Grasso;Carboidrati;kcal;Min (g);Max (g);Categoria;Pezzo (g)
Grana Padano;33;29;0;398;10;30;extra,secco;
Petto di pollo;22,5;2,6;0;120;120;280;proteina;
```

- I valori sono **per 100 g**.
- Delimitatore `,`, `;` o tab: rilevato automaticamente.
- Decimali con `.` o `,`.
- L'intestazione viene riconosciuta anche se il file ha un preambolo (es. `macros.csv`).
- Se `kcal` manca viene calcolata con `P×4 + C×4 + G×9`.
- Le colonne `Min`, `Max`, `Categoria` e `Pezzo` sono opzionali.
- L'import **unisce** i dati: alimenti con lo stesso nome vengono aggiornati, gli altri aggiunti.

Il pulsante **“Carica alimenti di esempio”** inserisce i 32 alimenti di
[`data/cibi.csv`](./data/cibi.csv) senza bisogno di importare nulla.
Puoi anche **esportare** il database corrente o **scaricare un modello** CSV vuoto.

## 🤖 Algoritmo greedy (score per macro)

Nel tab **Diario**, sotto i totali, l'app suggerisce **cosa mangiare per completare
la giornata** valutando **tutta la tabella dei cibi**:

1. Calcola il fabbisogno residuo `target − consumato` per kcal, proteine, grassi e carboidrati.
2. Per ogni alimento, con la sua porzione consigliata (`Max`, altrimenti `Min`, altrimenti 100 g),
   calcola uno **score per macro**: quanto la porzione copre il residuo di quel macro
   (100% = lo copre esattamente; chi sfora viene penalizzato con `1/ratio`).
3. Combina gli score in un **punteggio totale**, pesando di più i macro più carenti e in
   particolare le proteine (pesi: P 0.40, C 0.25, G 0.20, kcal 0.15).
4. Ordina e restituisce i **primi 10** alimenti, con la possibilità di riordinare per
   singolo macro (proteine / grassi / carboidrati / calorie) ed escludere i cibi già nel diario.

Ogni riga mostra lo score e i grammi apportati per ciascun macro, più un pulsante
**Aggiungi** per inserire la porzione nel diario.

## 🔄 Sincronizzare PC e telefono

`localStorage` è **per-dispositivo**: non si sincronizza da solo. Le opzioni, dalla più semplice:

1. **Backup JSON (implementato)** — nel tab in fondo all'app, *Esporta backup (.json)*
sul PC e *Importa backup* sul telefono (o viceversa). Sposta il file con iCloud Drive,
Google Drive, AirDrop, WhatsApp, email. Zero account, zero server.
2. **Servizio chiave-valore gratuito** (es. jsonbin.io) — l'app salva/legge un piccolo
JSON su cloud con una chiave. Automatico, ma richiede una registrazione.
3. **Backend** (Supabase/Firebase) — sync in tempo reale multi-dispositivo, ma più setup.

Il backup contiene **peso/calorie, diario e database cibi** (`{app, version, exportedAt, entries, foods, diary}`).

## Prossimi passi possibili

- [x] Tracciamento macro giornalieri (proteine, carboidrati, grassi)
- [ ] Promemoria settimanale per registrare peso e calorie
- [ ] Export dei dati (peso, calorie, diario) in formato CSV
