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
- 📓 **Diario alimentare**: aggiungi gli alimenti con i grammi e l'app calcola kcal + macro vs target

> I dati restano **sul dispositivo/browser** e non vengono inviati a nessun server.

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

## Prossimi passi possibili

- [x] Tracciamento macro giornalieri (proteine, carboidrati, grassi)
- [ ] Promemoria settimanale per registrare peso e calorie
- [ ] Export dei dati (peso, calorie, diario) in formato CSV
