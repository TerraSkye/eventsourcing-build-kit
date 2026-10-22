# Cart example

A shopping cart, modeled on the Event Modelers documentation's examples, and
the code the kit generates from it.

- `model/` — the input, as the Event Modelers CLI writes it to
  `.build-kit/.slices/`: `current_context.json`, `Cart/index.json`, and one
  `Cart/<folder>/slice.json` per slice.
- `app/` — the output of

  ```bash
  go -C templates/build-kit/gen run . -init -module example.com/cart \
      -slices "$PWD/examples/cart/model" -out "$PWD/examples/cart/app"
  (cd examples/cart/app && go mod tidy)
  ```

  untouched: no file in it was edited by hand (`scripts/regenerate.sh` and
  esgen's tests keep it that way).

| Slice | Type | Generated |
|---|---|---|
| Add Item | STATE_CHANGE | `slices/additem` — AddItem → CartCreated, ItemAdded; "cart is full", "cart already submitted" |
| Remove Item | STATE_CHANGE | `slices/removeitem` — RemoveItem → ItemRemoved; "item not in cart" |
| Clear Cart | STATE_CHANGE | `slices/clearcart` — ClearCart → CartCleared; "cart not found" |
| Submit Cart | STATE_CHANGE | `slices/submitcart` — SubmitCart → CartSubmitted; "cart is empty", "cart already submitted" |
| Cart Items | STATE_VIEW | `slices/cartitems` — rows keyed by cart and item; ItemRemoved removes a row, CartCleared all rows of the cart |
| Carts To Publish | STATE_VIEW | `slices/cartstopublish` — the todo list: CartSubmitted adds, CartPublished removes; `Claim` |
| Publish Cart | AUTOMATION | `slices/publishcart` (PublishCart → CartPublished, idempotent) and `automations/publishsubmittedcarts` |

The decide rules are derived from the 17 specifications and counted, not
modeled, state (`s.itemAdded >= 3`): that is where a person or the build loop
takes over — e.g. "item not in cart" really wants the set of item IDs.

```bash
cd app
go test ./...
go run .
```
