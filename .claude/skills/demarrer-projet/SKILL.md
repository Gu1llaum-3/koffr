---
name: demarrer-projet
description: Démarre un projet vide à partir de son cahier des charges — lit le CDC en entier, produit le registre d'exigences E-nn, les questions Q-nn et décisions D-nn, propose les ADR de cadrage et une ROADMAP par lots, remplit CLAUDE.md et le glossaire. Trois arrêts de validation, aucune décision prise à la place du propriétaire. À utiliser une fois, sur un dépôt où le kit de pilotage vient d'être copié, avec le chemin du CDC en argument.
---

# Démarrer un projet depuis son cahier des charges

Ce skill transforme un cahier des charges en documents de pilotage. Il **extrait, classe, propose
et demande** ; il ne tranche rien. Un CDC est écrit avant le projet, souvent sans mesurer et sans
tous les acteurs : c'est une source à lire de près, pas une vérité à exécuter. Tout ce qu'il ne dit
pas devient une question, tout ce qu'il contredit devient une décision en attente, et tout choix
structurant devient un ADR **proposé** que le propriétaire passe lui-même en accepté.

Le skill s'arrête trois fois. On ne saute aucun arrêt, même si le propriétaire dit « vas-y » : la
valeur du cadrage est dans ce qu'il relit.

## Préalables

- Le kit est copié à la racine (`METHODE.md`, `docs/README.md`, gabarits présents). Sinon, dire
  quoi copier et s'arrêter.
- Le CDC est dans `docs/cdc/` (ou son chemin est donné en argument). Un PDF ou un DOCX se lit avec
  l'outil de lecture par tranches de pages ; **on lit tout**, annexes comprises, avant d'écrire une
  ligne. Un CDC de 80 pages se lit en 80 pages.
- Si plusieurs documents (partie fonctionnelle, partie technique, annexes), on les lit tous et on
  note leurs versions et dates.

## Étape 1 — Lire et extraire (→ arrêt 1)

Produire, sans rien décider :

### `docs/cdc/analyse.md`

1. **Fiche du document** : titre, version, date, auteur, destinataires, ce qu'il couvre et ne
   couvre pas selon ses propres termes.
2. **Périmètre en une page** : à quoi sert le produit, pour qui, remplaçant quoi.
3. **Acteurs et rôles** : chaque type d'utilisateur, ce qu'il fait, combien ils sont si le CDC le
   dit. Ce sera le socle du modèle de droits.
4. **Objets métier** : chaque nom qui revient (client, commande, dossier…), sa définition selon le
   CDC, ses états s'il en a, ses relations. Ce sera le socle des modules et du glossaire.
5. **Flux et intégrations** : chaque système tiers, chaque échange (sens, format, fréquence,
   déclencheur), chaque écriture externe. Marquer celles qui **envoient** quelque chose hors du
   système : elles passeront par la porte de sortie.
6. **Contraintes** : délais, budget, réglementaire, hébergement, stack imposée, volumes, langues.
7. **Silences** : ce qu'un développeur devra savoir et que le CDC ne dit pas (règles de calcul sans
   formule, seuils sans valeur, « comme aujourd'hui » sans description, cas d'erreur, volumes,
   reprise de données existantes, qui administre). Chaque silence → une `Q-nn`.
8. **Contradictions** : entre deux §, entre le fonctionnel et le technique, entre le texte et une
   annexe. Chaque contradiction → une `Q-nn` (au rédacteur) ou une `D-nn` (au propriétaire).
9. **Ce que le CDC dit de ne pas faire** : hors périmètre explicite, reporté à une phase 2.

### `docs/cdc/exigences.md`

Le registre `E-nn`, au format de `docs/cdc/README.md`. Règles d'extraction :

- Une exigence = une phrase **vérifiable** (« l'acheteur peut… », « le système envoie… », « le
  montant est… »), reformulée au plus près du texte, avec son § et sa page.
- Une phrase du CDC qui contient plusieurs exigences est scindée. Une exigence répétée à deux
  endroits est une ligne avec deux sources.
- On n'interprète pas : une exigence ambiguë est reformulée telle quelle, marquée
  `en question (Q-nn)`.
- Le type et la priorité viennent du CDC ; s'il n'en donne pas, `à qualifier`.
- La colonne « Lot » reste vide à cette étape.
- Ordre : celui du CDC, pas un ordre logique inventé. Le lecteur doit pouvoir suivre avec le
  document ouvert.

### `docs/questions.md` et `docs/decisions.md`

Remplir les registres depuis les silences et contradictions. Chaque `Q-nn` est formulée **pour la
personne qui répond** (un utilisateur, pas un développeur), avec les réponses envisagées et ce que
chacune implique. Chaque `D-nn` dit qui tranche et ce qu'elle bloque.

### Glossaire

Dans `CLAUDE.md` § Glossaire : chaque objet métier et terme récurrent, en français tel que le CDC
l'écrit, avec l'identifiant anglais proposé. Un identifiant, une fois validé, ne change plus. Si le
CDC ou un système existant a déjà un identifiant anglais, on le garde.

### Arrêt 1

Présenter : le nombre d'exigences par type, les cinq plus structurantes, les questions bloquantes,
les contradictions, et **ce qu'on n'a pas compris**. Demander au propriétaire de relire
`exigences.md` et `analyse.md`. Ne pas passer à l'étape 2 sans un accord explicite ; intégrer ses
corrections d'abord.

## Étape 2 — Proposer les décisions de cadrage (→ arrêt 2)

Écrire des ADR en statut **proposé**, un par décision structurante, depuis `docs/adr/0000-template.md`,
en citant les `E-nn` qui les fondent. Le jeu minimal :

1. **Stack et versions** : si le CDC l'impose, l'ADR le constate et cite le § ; sinon il propose
   **une** stack avec ses raisons (compétences du propriétaire, contraintes d'hébergement, volumes),
   et liste les alternatives écartées. Si une brique est en version non stable, l'ADR prévoit un
   spike au lot 0. Ne jamais proposer une stack que le propriétaire ne maîtrise pas sans le dire.
2. **Langues** : pilotage en français, code en anglais (fixés par le kit) ; langue de l'interface
   et des messages selon le CDC et les utilisateurs.
3. **Modèle de données** : types des montants (décimal à précision fixe, jamais flottant), des
   dates (horodatage avec fuseau, fuseau applicatif nommé), des statuts (texte contraint, pas
   d'enum de base), colonnes d'audit, verrou optimiste, suppression logique seulement si le CDC
   l'exige.
4. **Écritures externes** : aucune hors production sans opt-in explicite, porte de sortie unique,
   configuration par environnement. Lister les flux sortants du CDC qui y passeront.
5. **Authentification et droits** : fournisseur d'identité selon le CDC ou `D-nn`, rôles issus des
   acteurs de l'analyse, droits `module:action` vérifiés dans le métier.
6. **Découpage en modules** : depuis les objets métier, une table appartient à un module. Proposer
   le code `MOD` de chacun.
7. Toute autre décision que le CDC force (multi-tenant, hors ligne, mobile, hébergement…).

Mettre à jour `docs/adr/README.md`. Remplir `ARCHITECTURE.md` § Modules avec les modules proposés
et leurs `E-nn` principales ; laisser le reste pour le lot 0.

### Arrêt 2

Présenter chaque ADR en trois lignes (décision, raison principale, ce qu'elle exclut). Le
propriétaire passe lui-même les statuts en `accepté`, amende ou refuse. **On ne code rien sur un
ADR proposé.** Ce qui est refusé devient une `D-nn` ou disparaît.

## Étape 3 — Découper en lots (→ arrêt 3)

Remplir `ROADMAP.md` :

- **Lot 0** : le gabarit du kit, complété par ce que la stack acceptée impose (spike, outils).
- **Lots 1 à N** : chaque lot livre **quelque chose qu'un utilisateur peut faire** (tranche
  verticale : écran → règle → base), pas une couche technique. Critères de découpage, dans
  l'ordre : ce qui débloque les autres (référentiels avant transactions), ce qui a le plus de
  valeur pour l'utilisateur référent, ce qui lève le plus d'inconnues tôt (une intégration
  incertaine se prototype au lot 1, pas au lot 6).
- Chaque lot : périmètre en `E-nn` (toutes affectées, aucune oubliée : la somme des lots = le
  registre, moins les `hors périmètre` et les `reportées`), critère de sortie **vérifiable**
  (ce qu'un utilisateur fait à la fin et comment on le constate), dépendances (`D-nn`, `Q-nn` à
  lever avant).
- **Lot final** : recette générale, répétitions de mise en service, retour arrière.
- Reporter la colonne « Lot » dans `exigences.md`. Les exigences que le CDC place en phase 2 vont
  dans `backlog.md` § « Reporté après la mise en service », avec leur `E-nn`.
- Pas de chiffrage en semaines. Si le CDC impose une date, l'inscrire en contrainte et ouvrir une
  `D-nn` sur ce qui tient dedans.
- Taille : trois à huit lots hors lot 0 et lot final. Un lot qui couvre plus de vingt exigences se
  scinde.

Compléter `CLAUDE.md` : la phrase de tête, les renvois, la langue de l'interface. Les §
Commandes, Conventions et Règles d'architecture restent avec leurs `{{…}}` : ils se remplissent au
lot 0 par `/implementer`.

### Arrêt 3

Présenter la roadmap en un tableau : lot, ce que l'utilisateur pourra faire, nombre d'exigences,
ce qui doit être tranché avant. Signaler les exigences non affectées s'il en reste (il ne doit pas
en rester). Le propriétaire valide, réordonne, scinde. Puis :

```
Cadrage terminé. Prochaine étape : `/ecrire-plan 0` pour le plan du lot 0.
```

## Ce que le skill ne fait jamais

- Écrire du code, un schéma de base, une maquette.
- Passer un ADR en `accepté`.
- Inventer une règle de calcul, un seuil, un cas d'erreur que le CDC ne donne pas : c'est une `Q-nn`.
- Résumer le CDC au lieu de le lire : chaque `E-nn` a un § et une page qu'on peut ouvrir.
- Enchaîner deux arrêts sans relecture, même à la demande.
- Chiffrer en semaines.

## Reprise

Si le skill est relancé sur un dépôt déjà cadré (registres non vides), il ne repart pas de zéro :
il relit `analyse.md` et `exigences.md`, compare au CDC (nouvelle version ?), et propose un **diff**
(exigences ajoutées, modifiées, retirées, avec le § de chacune) avant de toucher aux registres.
Une exigence retirée du CDC est barrée, pas supprimée.
