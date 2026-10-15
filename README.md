# Vasest

Vasest is an early testing framework for Kinmokusei applications and libraries. Tests and assertions are written in `.km` files. A small runner discovers `*.test.km` files, executes them with Kinmokusei, and returns a failing exit status when a file fails.

## Requirements

- Kinmokusei v0.4.3 or later
- Go 1.23 or later

## Install v0.1.0

Download the archive for your operating system and architecture from the [v0.1.0 release](https://github.com/puffball1567/vasest/releases/tag/v0.1.0). Extract it and put the `vasest` executable on your `PATH`. Run `vasest --version` to verify the installation. Kinmokusei and Go are still required to compile and run your tests.

In your Kinmokusei application project, add the matching library:

```sh
keika deps add github.com/puffball1567/vasest@v0.1.0
```

Use `vasest` as its import alias. For a source installation, clone the release and build the runner:

```sh
git clone --branch v0.1.0 https://github.com/puffball1567/vasest.git
cd vasest
mkdir -p build
keika build -o build/vasest cmd/vasest/main.km
```

Add the resulting `build` directory to your `PATH`, then run `vasest` from your application project.

## Write a test

Each test file is an executable Kinmokusei source file. A suite can contain several named tests:

```ts
import { Suite, expectEqual, expectTrue, runSuiteAndExit } from "vasest";

function main(): void {
  runSuiteAndExit("arithmetic", (suite: Suite): void => {
    suite.beforeAll((): void => {
      // Set up resources shared by this suite.
    });
    suite.afterAll((): void => {
      // Release shared resources once.
    });
    suite.beforeEach((): void => {
      // Set up state for each selected test.
    });
    suite.afterEach((): void => {
      // Cleanup also runs when an assertion fails.
    });
    suite.test("addition", (): void => {
      expectEqual(2 + 3, 5);
      expectTrue(5 > 0);
    });
  });
}
```

Suite and test names must contain text and use valid UTF-8. Unicode spellings remain distinct, including composed/decomposed text and the replacement character `�`; assertions still accept native string values containing arbitrary bytes.

Name the file `tests/arithmetic.test.km`. The project must have Vasest as a Kinmokusei dependency with the import alias `vasest`.

For a matcher style familiar to TypeScript test authors, import `expect`:

```ts
import { Suite, expect, runSuiteAndExit } from "vasest";

function main(): void {
  runSuiteAndExit("arithmetic", (suite: Suite): void => {
    suite.test("addition", (): void => {
      expect(2 + 3).toEqual(5);
      expect([1, 2]).toEqual([1, 2]);
      expect("ready").not.toEqual("pending");
      expect(true).toBe(true);
    });
  });
}
```

`expect(value)` infers the value type; `toEqual(expected)` and `toBe(expected)` require that same type. Type inference is optional: use `expect<MyType>(value)` or `new Expectation<MyType>(value)` when the type should be explicit. Numeric widths, nominal defined types, and generic arguments retain Kinmokusei's normal assignability rules. The compiler rejects incompatible expected values; assertions do not coerce them into a shared JavaScript value type.

`toEqual` uses the same deep equality and string diagnostics as `expectEqual`. `toBe` compares comparable values and pointer/class identity; scalar NaN matches NaN and positive zero differs from negative zero. Slices, maps, functions, and values containing non-comparable members are rejected by `toBe`, including its negated form; use `toEqual` for collection contents.

`toBeNil()` accepts nil interfaces and typed nil pointers, slices, maps, functions, or channels. Empty collections, empty strings, zero, and false are non-nil. All three matchers support `.not`, for example `expect(value).not.toBeNil()`. Negation does not change a saved matcher: its positive form remains usable. Matcher failures report the matcher call's location and use the existing cleanup, reporter, and failure-history behavior. The function-style assertions remain available alongside matchers.

Use `toSatisfy(predicate)` for domain-specific checks. The predicate accepts the matcher's exact `T` and returns `boolean`; incompatible parameter types, numeric widths, nominal types, and return types are rejected by the compiler. Explicit generic arguments and `Expectation<T>` constructors work here too:

```ts
import { Expectation, expect } from "vasest";

type UserID = distinct int64;

// Inside a test body:
const id = UserID(42);
const isPositive: (value: UserID) => boolean = (value: UserID): boolean => {
  return value > UserID(0);
};
expect<UserID>(id).toSatisfy(isPositive);
new Expectation<UserID>(id).not.toSatisfy((value: UserID): boolean => {
  return value < UserID(0);
});
```

Each check invokes the predicate exactly once, passing the native value without copying or coercing it. A false return fails the positive matcher; a true return fails `.not.toSatisfy`. Nil predicates fail in both forms. Predicate throws, runtime skips, and native panics propagate unchanged, including any existing failure location, and normal cleanup still runs. Negation only reverses a returned boolean. Mismatch and nil-predicate failures identify the matcher call; predicates can use `withTestLocation` to attach an explicit location to their own thrown errors.

For ordered values use `expectOrdered<T>(value)` or `new OrderedExpectation<T>(value)`. The type must satisfy Go's `cmp.Ordered` constraint: integer, floating-point or string types, including native defined types such as `time.Duration`. `toBeGreaterThan`, `toBeGreaterThanOrEqual`, `toBeLessThan`, and `toBeLessThanOrEqual` each take an expected value assignable to the same `T` and support `.not`:

```ts
import { OrderedExpectation, expectOrdered } from "vasest";

type UserID = distinct int64;

// Inside a test body:
const id = UserID(1152921504606846977);
expectOrdered<UserID>(id).toBeGreaterThan(UserID(1152921504606846976));
expectOrdered(id).toBeLessThanOrEqual(id);
new OrderedExpectation<UserID>(id).not.toBeLessThan(id);
expectOrdered<string>("金木犀").toBeGreaterThan("木");
```

Comparisons use native `<`, `<=`, `>`, and `>=` without numeric conversion. Large integers retain their precision; incompatible numeric widths and nominal types, and unordered types such as booleans or collections, are rejected by the compiler. Strings follow native byte-wise lexicographic ordering. Positive and negative zero compare equal, and infinities follow native ordering. Any comparison involving NaN returns false, so each negated ordering matcher passes for NaN; negation reverses the comparison result rather than replacing the operator with its opposite. Failures include both values, the requested relation, and the matcher call's location.

For floating-point values use `expectFloat<T>(value)` or `new FloatExpectation<T>(value)`. The exported `Float` constraint accepts native `float32`, `float64`, and defined types with either underlying type. `toBeCloseTo(expected, tolerance)` requires an explicit, inclusive absolute tolerance; both arguments must be assignable to the same `T`. `toBeNaN()` and `toBeFinite()` check native floating-point properties. All three support `.not`, inference and explicit types:

```ts
import { FloatExpectation, expectFloat } from "vasest";
import go math from "math";

type Distance = distinct float32;

// Inside a test body:
const measured = Distance(1.125);
expectFloat<Distance>(measured).toBeCloseTo(Distance(1), Distance(0.125));
new FloatExpectation<Distance>(measured).toBeFinite();
expectFloat(measured).not.toBeNaN();
expectFloat(1.5).not.toBeCloseTo(1.0, 0.125);
expectFloat(math.NaN()).toBeNaN();
expectFloat(math.Inf(1)).not.toBeFinite();
```

Approximate comparison keeps subtraction and its rounding in the native `T`, including `float32`. Signed zeros compare within zero tolerance, and subnormal values remain finite. Finite operands whose subtraction overflows exceed every finite tolerance. A tolerance must be finite and non-negative, and actual/expected values must be finite; invalid values fail even when negated. Use `toBeNaN` or `toBeFinite` to inspect special values. The compiler rejects incompatible widths and nominal types, and integers, strings, booleans or collections cannot instantiate these matchers. Failures retain the fluent call's location and normal cleanup, reporting and failed-case rerun behavior.

For text-specific assertions use `expectString(text).toContain(fragment)`, `.toMatch(pattern)`, or `.toHaveLength(expected)`. For slices use `expectSlice<T>(values).toContain(element)` or `.toHaveLength(expected)`; the element must be assignable to `T`, including when negated. Slice containment uses Go deep equality, supporting nested collections and value structs. These matchers all support `.not`. Text patterns use Go's RE2 syntax; malformed patterns fail even when negated. Lengths must be non-negative `int` values. String length counts UTF-8 bytes, following Kinmokusei's `len`, while slice length counts elements. Nil and empty slices both have length zero, but remain distinct in `toEqual`.

```ts
import { Expectation, SliceExpectation, expect, expectSlice, expectString } from "vasest";

type UserID = distinct int;

// Inside a test body:
const id = UserID(42);
expect<UserID>(id).toEqual(UserID(42));
new Expectation<UserID>(id).not.toEqual(UserID(43));
expectSlice<UserID>([id]).toContain(UserID(42));
expectString("金木犀").toContain("木");
expectString("金木犀").toHaveLength(9);

const empty: UserID[] = [];
expectSlice<UserID>(empty).toHaveLength(0);
new SliceExpectation<UserID>(empty).not.toContain(id);
expect<error>(nil).toBeNil();
```

Give empty collections a typed binding before passing them to a matcher. Kinmokusei v0.4.3 does not always carry an explicit generic call's type into an empty array literal. Typed bindings, explicit type arguments, and the exported matcher constructors provide a verified path without depending on that inference. `StringExpectation` is also available for direct construction. The specialized string/slice matchers provide their text/collection operations; use the general `expect<T>` for ordered equality, identity, and nil checks.

For literal string boundaries, use `toStartWith(prefix)` and `toEndWith(suffix)`, including `.not`:

```ts
import { StringExpectation, expectString } from "vasest";

// Inside a test body:
const contentType = "application/json; charset=utf-8";
expectString(contentType).toStartWith("application/json");
new StringExpectation(contentType).toEndWith("utf-8");
expectString("[user]+.json").toStartWith("[user]+");
expectString("[user]+.json").not.toEndWith(".txt");
const startsWith: (prefix: string) => void = expectString(contentType).toStartWith;
startsWith("application/");
```

Fragments are literal text and need no regex escaping. Comparisons use native string bytes, preserving case, Unicode normalization differences, NUL and invalid UTF-8. Empty fragments match every string, including the empty string; their negation fails. Arguments must be assignable to `string`, with no automatic conversion of other types. Constructors and typed method bindings are supported. Saved matchers retain the captured string, and failures quote the values and preserve the fluent call's location, cleanup, reports and failed-case reruns.

For typed sequences, `expectSlice<T>(values).toStartWith(prefix)` and `.toEndWith(suffix)` check contiguous elements at the corresponding boundary. Both fragments have type `T[]`, and `.not` negates the complete boundary match:

```ts
import { SliceExpectation, expectSlice } from "vasest";

type UserID = distinct uint64;

// Inside a test body:
const first = UserID(18446744073709551615);
const events: UserID[] = [first, UserID(42), UserID(43)];
const matcher: SliceExpectation<UserID> = expectSlice<UserID>(events);
matcher.toStartWith([first, UserID(42)]);
matcher.toEndWith([UserID(42), UserID(43)]);
matcher.not.toStartWith([UserID(42)]);
const checkEnd: (expected: UserID[]) => void = matcher.toEndWith;
checkEnd([UserID(43)]);
```

Explicit generics, typed bindings, direct constructors and inferred calls are supported. Order and repeated elements must match at the boundary; a longer fragment never matches. Nil and empty fragments match every slice, including nil and empty slices, so their negation fails. Each corresponding element uses native deep equality, preserving nested nil/empty differences, native integer widths, nominal types, references, cycles and floating-point behavior. A top-level NaN element does not match itself, including when the fragment shares the actual slice's backing storage; identity shortcuts inside nested elements follow native deep equality. Checks do not modify either slice, take at most `len(fragment)` comparisons, and allocate no matching workspace. Saved matchers retain the captured slice and observe later changes to its backing elements. Mismatches include both slices with cycle-safe diagnostics and preserve the fluent or explicit failure location, cleanup, reporters and exact failed-case reruns.

Use `toHaveElement(index, expected)` to check the value at a zero-based slice position, or `toHaveElementSatisfying(index, predicate)` for a typed condition on that value:

```ts
import { SliceExpectation, expectSlice } from "vasest";

type UserID = distinct uint64;

// Inside a test body:
const ids: UserID[] = [UserID(18446744073709551615), UserID(42)];
const matcher: SliceExpectation<UserID> = expectSlice<UserID>(ids);
matcher.toHaveElement(1, UserID(42));
matcher.toHaveElementSatisfying(0, (value: UserID): boolean => {
  return value == UserID(18446744073709551615);
});
matcher.not.toHaveElement(2, UserID(0));
const checkElement: (index: int, expected: UserID) => void = matcher.toHaveElement;
checkElement(1, UserID(42));
```

Both support `.not`, inferred or explicit element types, constructors and typed method bindings. The index has native type `int`; expected values and predicate parameters retain the exact element type. Negative indices are invalid even when negated. Nonnegative positions outside the slice fail a positive check and pass its negation without an index panic. Missing positions remain distinct from present zero or nil values. Equality uses native deep equality, including nested nil/empty, NaN, function and cycle semantics; diagnostics distinguish missing positions from mismatched values and safely summarize cycles.

A nil predicate fails either form before reading an element, even at a missing position. Present positions read their native value once and invoke the predicate once; missing positions invoke no callback. Callback mutations do not replace that selected value, while references retain their native identity and mutations. Saved matchers observe changes to their captured backing elements. Predicate mismatches do not format application values. Callback throws, skips, attached locations and native panics propagate unchanged with normal cleanup. Matcher errors retain their fluent or explicit failure location, all reporters, bail and exact failed-case reruns.

Use `expectSlice<T>(actual).toEqualUnordered(expected)` to compare the same elements without requiring their outer order. Duplicate counts matter: `[1, 1, 2]` matches `[2, 1, 1]`, but differs from `[1, 2, 2]`. The expected slice retains exactly the same element type `T`; inference, explicit type arguments, direct `SliceExpectation<T>` constructors and `.not` all work:

```ts
import { SliceExpectation, expectSlice } from "vasest";

type UserID = distinct int64;

// Inside a test body:
const actual: UserID[] = [UserID(42), UserID(43), UserID(42)];
const expected: UserID[] = [UserID(43), UserID(42), UserID(42)];
expectSlice<UserID>(actual).toEqualUnordered(expected);
new SliceExpectation<UserID>(actual).toEqualUnordered(expected);
expectSlice(actual).not.toEqualUnordered([UserID(43), UserID(43), UserID(42)]);
```

Each element is compared using native deep equality, supporting structs, classes, maps and nested slices without a `comparable` constraint. Only the outer order is ignored; nested slice order stays significant. Nil and empty slices differ, including nested values. Floating-point comparisons follow native deep equality: signed zeros match, while a NaN element does not match itself, even when both arguments use the same outer slice. The matcher neither sorts nor modifies either input. Saved matchers retain the backing slice and observe later element mutations. Comparisons take up to quadratic time in the number of elements, plus the cost of deep comparisons; mismatch locations identify the fluent call.

Use `toContainAll(expected)` when additional elements are allowed. Each required element must match a distinct actual element using native deep equality, ignoring only the outer order. Duplicate counts matter: `[1, 2, 1, 3]` contains `[1, 1]`, while `[1, 2, 3]` does not. `.not.toContainAll` reverses the whole inclusion check, so it passes if any required element or duplicate is missing:

```ts
// Inside a test body:
const actual: int64[] = [int64(42), int64(43), int64(42), int64(44)];
const required: int64[] = [int64(42), int64(42)];
expectSlice<int64>(actual).toContainAll(required);
new SliceExpectation<int64>(actual).toContainAll(required);
expectSlice(actual).not.toContainAll([int64(42), int64(42), int64(42)]);
```

An empty or nil expected slice requires no elements and therefore matches any actual slice, including nil or empty; its negation fails. Nil and empty nested elements remain distinct. Both inputs retain the same element type, supporting explicit generics, inferred calls, constructors and typed matcher bindings without a `comparable` constraint. Native deep equality preserves numeric widths, nominal types, references, cyclic values, nested order and floating-point behavior. Matching neither sorts nor mutates either input, and saved matchers observe later backing-element changes. It takes up to `len(actual) * len(expected)` deep comparisons and linear bookkeeping space; mismatch diagnostics include both slices and identify the fluent call.

Use `expectSlice<T>(values).toContainSequence(expected)` to find a contiguous sequence anywhere in a slice. The expected sequence has type `T[]`; `.not` requires that the complete sequence is absent:

```ts
import { SliceExpectation, expectSlice } from "vasest";

// Inside a test body:
const events: string[] = ["connected", "started", "completed", "closed"];
const matcher: SliceExpectation<string> = expectSlice<string>(events);
matcher.toContainSequence(["started", "completed"]);
matcher.not.toContainSequence(["started", "closed"]);
const check: (expected: string[]) => void = matcher.toContainSequence;
check(["completed", "closed"]);
```

Order, adjacency and repeated elements matter. For example, `[1, 2, 1]` does not contain `[1, 1]`; `[1, 1, 2]` does contain `[1, 2]`, even though the first candidate starts with the same value and fails. Empty or nil expected sequences match every slice, including nil and empty slices, so their negation fails. Matching uses native deep equality for each element and follows the same nested nil/empty, NaN, function, reference and cycle semantics as slice boundary matchers. Explicit generics, constructors, inferred calls and typed method bindings retain the exact element type. Neither input is modified; saved matchers observe backing-element changes. Searching takes up to `(len(actual) - len(expected) + 1) * len(expected)` deep comparisons when the expected sequence fits and uses no matching workspace. Diagnostics include both slices, preserve fluent or explicit locations, and follow normal cleanup, reporting, bail and failed-case rerun rules.

Use `toContainInOrder(expected)` when intervening elements are allowed but the required elements must appear in order. Each required element consumes a distinct actual position, so duplicate counts still matter:

```ts
import { SliceExpectation, expectSlice } from "vasest";

// Inside a test body:
const events: string[] = ["connected", "started", "progress", "completed", "closed"];
const matcher: SliceExpectation<string> = expectSlice<string>(events);
matcher.toContainInOrder(["started", "completed"]);
matcher.not.toContainInOrder(["completed", "started"]);
const checkOrder: (expected: string[]) => void = matcher.toContainInOrder;
checkOrder(["connected", "completed", "closed"]);
```

`[1, 2, 1]` contains `[1, 1]` in order, while `[1, 2, 3]` does not contain `[3, 1]` or `[1, 1]`. `.not` negates the complete ordered inclusion. Empty or nil requirements match every slice, including nil and empty slices; their negation fails. Element types remain exact through inferred or explicit generics, constructors and bound methods. Matching uses native deep equality without a `comparable` constraint, preserving nested nil/empty values, integer widths, nominal types, references, cycles, function values and NaN semantics. It scans actual positions once, makes at most `len(actual)` deep comparisons, stops when all requirements match, and uses no matching workspace. Neither input is modified; saved matchers observe backing-element changes. Failure diagnostics, locations, cleanup, reporting, bail and exact reruns follow the same rules as other slice matchers.

For conditions on slice elements, use `toSatisfyAny(predicate)` to require at least one match and `toSatisfyAll(predicate)` to require every element to match. The predicate takes the exact element type `T` and returns `boolean`. Inference, explicit type arguments, and `SliceExpectation<T>` constructors are all supported:

```ts
import { SliceExpectation, expectSlice } from "vasest";

type UserID = distinct int64;

// Inside a test body:
const ids: UserID[] = [UserID(42), UserID(43)];
const isPositive: (value: UserID) => boolean = (value: UserID): boolean => {
  return value > UserID(0);
};
expectSlice<UserID>(ids).toSatisfyAll(isPositive);
expectSlice(ids).toSatisfyAny((value: UserID): boolean => { return value == UserID(42); });
new SliceExpectation<UserID>(ids).not.toSatisfyAny((value: UserID): boolean => {
  return value < UserID(0);
});
```

Both support `.not`, which reverses the aggregate result: `.not.toSatisfyAny` requires no matching element; `.not.toSatisfyAll` requires at least one nonmatching element. Nil and empty slices contain no matches and satisfy all conditions, so `toSatisfyAll` and `.not.toSatisfyAny` pass without invoking the predicate. A nil predicate fails all four forms, even on an empty slice.

Checks visit elements in native slice order, call the predicate once per visited element, and stop at the first decisive match for `Any` or nonmatch for `All`. Values retain their native types and reference semantics; saved matchers observe mutations to existing backing elements. Each check evaluates afresh without caching or retrying. Callback throws, runtime skips, attached failure locations, and native panics propagate unchanged with normal cleanup. Matcher mismatches and nil-predicate failures identify the fluent call; mismatch messages include the number of elements checked and the slice length.

Use `toSatisfyAnyIndexed(predicate)` or `toSatisfyAllIndexed(predicate)` when the condition also depends on the element's position. The callback receives `(value: T, index: int)`, with a zero-based index:

```ts
import { SliceExpectation, expectSlice } from "vasest";

type UserID = distinct int64;

// Inside a test body:
const ids: UserID[] = [UserID(42), UserID(42), UserID(43)];
const matcher: SliceExpectation<UserID> = expectSlice<UserID>(ids);
matcher.toSatisfyAnyIndexed((value: UserID, index: int): boolean => {
  return index == 1 && value == UserID(42);
});
const validPosition: (value: UserID, index: int) => boolean = (value: UserID, index: int): boolean => {
  return (index < 2 && value == UserID(42)) || (index == 2 && value == UserID(43));
};
const checkAll: (predicate: (value: UserID, index: int) => boolean) => void = matcher.toSatisfyAllIndexed;
checkAll(validPosition);
matcher.not.toSatisfyAnyIndexed((value: UserID, index: int): boolean => { return index > 2; });
```

Both support `.not`, inferred or explicit element types, constructors and typed method bindings. Element, index, callback arity/result and binding types are checked by the compiler; the index has native type `int`. Each visited position invokes the callback once, in ascending order, stopping at the first match for `Any` or nonmatch for `All`. Negation reverses the aggregate result. Nil and empty slices invoke no callbacks: `toSatisfyAllIndexed` and `.not.toSatisfyAnyIndexed` pass. A nil callback fails all four forms before evaluation. Values retain native types and references, and later positions read changes made to their backing elements by earlier callbacks. Saved matchers retain their original slice header; reassigning the caller's slice does not replace that header. Fresh and nested checks start again at index zero. Callback throws, skips, attached locations and native panics propagate unchanged with normal cleanup. Mismatches and nil callbacks report the matcher call's location without formatting application values.

For maps use `expectMap<K, V>(values)` or `new MapExpectation<K, V>(values)`. `K` must satisfy Kinmokusei's `comparable` constraint. `toHaveKey(key)` checks key presence, `toHaveEntry(key, expected)` requires a present key and deep-equal value, and `toHaveLength(expected)` checks the entry count. All three support `.not`; a missing key passes `.not.toHaveEntry`, including when the expected value is zero or nil.

```ts
import { MapExpectation, expectMap } from "vasest";

type UserID = distinct int;

// Inside a test body:
const scores: Map<UserID, int> = makeMap[UserID, int]();
scores[UserID(42)] = 0;
expectMap<UserID, int>(scores).toHaveKey(UserID(42));
expectMap<UserID, int>(scores).toHaveEntry(UserID(42), 0);
expectMap(scores).not.toHaveEntry(UserID(43), 0);
expectMap<UserID, int>(scores).toHaveEntrySatisfying(UserID(42), (value: int): boolean => {
  return value >= 0;
});
expectMap(scores).not.toHaveEntrySatisfying(UserID(43), (value: int): boolean => {
  return value == 0;
});
new MapExpectation<UserID, int>(scores).toHaveLength(1);

const absent: Map<UserID, int> = nil;
expectMap<UserID, int>(absent).toHaveLength(0);
```

Key and value types are inferred from the map or supplied explicitly. Incompatible keys, values, nominal types, and numeric widths are rejected by the compiler; non-comparable key types cannot instantiate the matcher. Nil and empty maps both have length zero and no keys, but remain distinct under general `expect(...).toEqual(...)`. Keys use native map equality, including class/pointer identity; values use Go deep equality, including nested collections and nil-versus-empty distinctions. Saved matchers observe mutations to the same map without copying it. Interface keys with unhashable dynamic values retain native Go panic behavior and normal cleanup. Map mismatch diagnostics distinguish missing keys from present zero-valued entries; negative lengths fail even when negated.

Use `toHaveEntries(expected)` to require several entries while allowing extra entries in the actual map. The expected map retains the same key and value types; inference, explicit type arguments, constructors and typed method bindings work:

```ts
// Inside a test body, using the typed scores map above:
const required: Map<UserID, int> = makeMap[UserID, int]();
required[UserID(42)] = 0;
expectMap<UserID, int>(scores).toHaveEntries(required);
new MapExpectation<UserID, int>(scores).toHaveEntries(required);
const checkEntries: (expected: Map<UserID, int>) => void = expectMap(scores).toHaveEntries;
checkEntries(required);
required[UserID(43)] = 1;
expectMap(scores).not.toHaveEntries(required);
```

Each required key must be present and its value must match using native deep equality, just as in `toHaveEntry`. `.not` reverses the whole requirement and passes when any key is missing or any value differs. Nil and empty expected maps require no entries and therefore match every actual map; their negation fails. Present zero/nil values remain distinct from missing keys, and nil versus empty nested values remain distinct. Keys keep native identity and NaN-key lookup behavior; NaN values retain deep equality behavior even when both arguments are the same outer map. Checks neither copy nor modify either map, and saved matchers observe later mutations. Work is proportional to required entries plus their native comparisons, with short circuit evaluation on the first mismatch. Diagnostics include both maps, safely summarize cyclic graphs and retain the fluent call's location.

Use `toHaveEntrySatisfying(key, predicate)` for a domain-specific condition on one map value. The key retains type `K`, and the predicate accepts exactly `V` and returns `boolean`. Explicit `expectMap<K, V>` calls, inferred calls, and `MapExpectation<K, V>` constructors all work. Incompatible key, predicate parameter, numeric width, nominal and result types are rejected by the compiler.

A present entry invokes the predicate exactly once per check, including present zero or nil values. A missing key fails the positive matcher and passes `.not.toHaveEntrySatisfying` without invoking the predicate. For present entries, `.not` reverses only the returned boolean. A nil predicate fails both forms before lookup, even for nil maps or missing keys. Each check looks up the entry once; changes made inside the callback do not cause another lookup or invocation. Native value and reference semantics are preserved. Callback throws, runtime skips, attached failure locations and native panics propagate unchanged with normal cleanup; interface keys with unhashable values retain native lookup panic behavior. Missing-key, mismatch and nil-predicate diagnostics identify the fluent call.

Use Map's `toSatisfyAny(predicate)` and `toSatisfyAll(predicate)` to check whole entries. The callback receives `(key: K, value: V)` with the map's native types:

```ts
// Inside a test body, using the typed scores map above:
const validEntry: (key: UserID, value: int) => boolean = (key: UserID, value: int): boolean => {
  return key > UserID(0) && value >= 0;
};
expectMap<UserID, int>(scores).toSatisfyAll(validEntry);
expectMap(scores).toSatisfyAny((key: UserID, value: int): boolean => { return value == 0; });
new MapExpectation<UserID, int>(scores).not.toSatisfyAny((key: UserID, value: int): boolean => {
  return key == UserID(0);
});
```

Both support `.not`, which reverses the aggregate result. `.not.toSatisfyAny` requires no matching entry; `.not.toSatisfyAll` requires at least one nonmatching entry. Nil and empty maps contain no matches and satisfy all conditions, so `toSatisfyAll` and `.not.toSatisfyAny` pass without calling the predicate. Nil predicates fail all four forms before iteration. Explicit generic arguments, typed callback bindings and constructors keep inference optional; incompatible key, value, nominal, numeric-width and result types are rejected by the compiler.

Checks call the predicate once per visited entry and stop at the first decisive match for `Any` or nonmatch for `All`. Entries use native Map iteration, whose order is unspecified. Keys and values come directly from each entry, including NaN keys and zero/nil values, without a second lookup or copying the map. Saved matchers re-read the same map on each check. Callback mutations retain native Map iteration behavior: entries deleted before visitation are skipped, and new entries may or may not be visited. Values and references retain their native semantics. Callback throws, runtime skips, attached locations and native panics propagate unchanged with normal cleanup. Validation and mismatch failures identify the fluent call; mismatch diagnostics include the number of visited entries without formatting their keys or values.

Use `expectErrorValue(err)` for error-chain matchers. It accepts native Go errors and Kinmokusei classes implementing `error`; `new ErrorExpectation(err)` is also available:

```ts
import { expectErrorValue, expectString } from "vasest";
import go fmt from "fmt";
import go os from "os";

// Inside a test body:
const original = os.PathError { Op: "open", Path: "missing", Err: os.ErrNotExist };
const wrapped = fmt.Errorf("load: %w", &original);
expectErrorValue(wrapped).not.toBeNil();
expectErrorValue(wrapped).toMatchCause(os.ErrNotExist);

let found: *os.PathError = nil;
expectErrorValue(wrapped).toMatchType<*os.PathError>(&found);
expectString(found.Path).toContain("missing");
expectErrorValue(nil).not.toMatchType<*os.PathError>();
```

`toMatchCause(target)` and `.not.toMatchCause(target)` use Go's `errors.Is`, retaining identity, wrapping, joined errors, and custom `Is` behavior. Equal messages do not imply equal causes. `toMatchType<T>(&target)` uses `errors.As`, assigning the extracted value to the typed destination. The type can be inferred from the destination or supplied explicitly. As with `expectErrorAs`, a destination must be a non-nil pointer to an error type or interface; invalid destinations fail before matching, including when the error is nil. Ordinary mismatches preserve the destination; custom `As` methods retain their native ability to change it even when returning false.

The negated `.not.toMatchType<T>()` requires an explicit type and checks that no assignable error exists using its own destination, leaving caller variables untouched. It supports error types and interfaces, including non-error interfaces such as `fmt.Stringer`; invalid types fail even when the error is nil. Both forms follow custom `As` behavior and treat matching typed nil errors as matches.

Use `toMatchTypeSatisfying<T>(predicate)` to check the extracted type and its contents together without declaring a destination:

```ts
// Inside a test body, using the imports and wrapped error above:
const isMissingPath: (value: *os.PathError) => boolean = (value: *os.PathError): boolean => {
  return value != nil && value.Path == "missing";
};
expectErrorValue(wrapped).toMatchTypeSatisfying<*os.PathError>(isMissingPath);
expectErrorValue(wrapped).toMatchTypeSatisfying(isMissingPath);
expectErrorValue(wrapped).not.toMatchTypeSatisfying<*os.PathError>((value: *os.PathError): boolean => {
  return value != nil && value.Op == "write";
});
```

The predicate receives the first value selected by native `errors.As`, exactly once per check. A false predicate does not search for another error of the same type. Missing types fail the positive matcher and pass `.not` without invoking the predicate; negation reverses the combined type-and-predicate result. Error types and interfaces, including non-error interfaces, retain native reference and value semantics, and matching typed nil values reach the predicate. Explicit type arguments, typed callback bindings and direct error matcher constructors are supported. Nil predicates and invalid types fail before matching, even for nil errors and negated checks. Mismatch diagnostics do not format the original error. Predicate and custom `As` throws, skips, attached locations and native panics propagate unchanged with normal cleanup; assertion failures identify the fluent call.

Error matcher `toBeNil()` checks whether the error interface itself is nil, following Kinmokusei/Go's `err == nil`. An interface containing a typed nil error is non-nil and passes `.not.toBeNil()`. The general `expect(value).toBeNil()` checks nil-backed values, including typed nil pointers. Error matchers preserve call locations, cleanup, all reporters, and exact failed-case reruns. Native panics in custom `Is`, `As`, or `Unwrap` methods propagate after normal cleanup.

Use `expectFunction(body)` or `new FunctionExpectation(body)` to assert Kinmokusei `throw` behavior. The callback has type `() => void` and is not invoked during construction. Each matcher call invokes it exactly once; saving a matcher does not cache its result:

```ts
import { FunctionExpectation, expectErrorValue, expectFunction } from "vasest";
import go fmt from "fmt";
import go os from "os";

// Inside a test body:
const original = os.PathError { Op: "open", Path: "missing", Err: os.ErrNotExist };
const load: () => void = (): void => { throw fmt.Errorf("load: %w", &original); };
const thrown = expectFunction(load).toThrow();
expectErrorValue(thrown).toMatchCause(os.ErrNotExist);
expectFunction(load).toThrowMessage("missing");
expectFunction(load).toThrowCause(os.ErrNotExist);

let found: *os.PathError = nil;
new FunctionExpectation(load).toThrowType<*os.PathError>(&found);
expectFunction(load).not.toThrowType<*os.LinkError>();
expectFunction((): void => {}).not.toThrow();
```

`toThrow()` returns the original thrown error. `toThrowMessage(fragment)` checks a non-empty message substring. `toThrowCause(target)` follows `errors.Is`, and `toThrowType<T>(&target)` extracts through `errors.As`, with inferred or explicit destination types. All positive forms require a throw. `.not.toThrow()` requires normal return; `.not.toThrowMessage(fragment)`, `.not.toThrowCause(target)`, and `.not.toThrowType<T>()` permit normal return or a throw that does not match the specified condition. The negated type check uses a private destination.

Use `toThrowSatisfying(predicate)` for an application-specific condition on the thrown error. The predicate has type `(exception: error) => boolean` and receives the original native error exactly once when the body throws. A typed binding and direct `FunctionExpectation` or `NegatedFunctionExpectation` constructors are supported:

```ts
import { expectFunction, FunctionExpectation } from "vasest";
import go errors from "errors";
import go fmt from "fmt";

// Inside a test body:
const cause = errors.New("resource unavailable");
const load: () => void = (): void => { throw fmt.Errorf("load: %w", cause); };
const isUnavailable: (exception: error) => boolean = (exception: error): boolean => {
  return errors.Is(exception, cause);
};
expectFunction(load).toThrowSatisfying(isUnavailable);
new FunctionExpectation(load).not.toThrowSatisfying((exception: error): boolean => {
  return exception == nil;
});
```

The positive matcher requires a throw and a true predicate result. `.not.toThrowSatisfying` permits normal return, without invoking the predicate, or a throw with a false predicate result. Each check invokes the body once and evaluates afresh. Nil predicates and nil bodies fail at the matcher call before either callback executes. Nil thrown payloads are distinct from normal return; typed nil errors, error chains and application error references retain native semantics. The matcher does not call `Error`, `Is` or `As` implicitly, including when reporting a mismatch; use those operations inside the predicate when desired.

Predicate throws, attached locations, runtime skips and native panics propagate unchanged. A body runtime skip is a thrown outcome, following the other function matchers; a predicate runtime skip skips the enclosing test. Matcher mismatches and validation failures retain the fluent call's location, while ordinary predicate errors receive no invented position. Normal suite cleanup, reporters, bail and failed-case reruns apply.

Use `toThrowTypeSatisfying<T>(predicate)` to inspect an extracted error's contents in the same throw assertion:

```ts
import { expectFunction } from "vasest";
import go os from "os";

// Inside a test body:
const original = os.PathError { Op: "open", Path: "missing", Err: os.ErrNotExist };
const load: () => void = (): void => { throw &original; };
const isMissingPath: (value: *os.PathError) => boolean = (value: *os.PathError): boolean => {
  return value != nil && value.Path == "missing";
};
expectFunction(load).toThrowTypeSatisfying<*os.PathError>(isMissingPath);
expectFunction(load).toThrowTypeSatisfying(isMissingPath);
expectFunction(load).not.toThrowTypeSatisfying<*os.PathError>((value: *os.PathError): boolean => {
  return value != nil && value.Op == "write";
});
```

Each check executes the body once and uses native `errors.As` to select the first assignable value. The predicate runs once when extraction succeeds; a false predicate does not continue searching the chain. The positive matcher requires a throw, a matching type and a true predicate. `.not` permits normal return, a missing type, or a false predicate; normal return and missing types skip the predicate. Error types and interfaces, including non-error interfaces and matching typed nil values, preserve native value and reference semantics. A nil thrown payload is a throw but has no assignable error value. Explicit type arguments, inferred typed callback bindings and direct function matcher constructors are available. Invalid types, nil predicates and nil bodies fail before executing callbacks, even when negated. Diagnostics do not format the original error. Custom `As` and predicate throws, skips, locations and native panics propagate unchanged with normal cleanup; body skip signals remain thrown outcomes.

Empty message fragments, invalid type arguments, and invalid/nil type destinations fail before invoking the callback, including in negated assertions. A nil callback fails when any matcher is called. Ordinary extraction mismatches preserve the destination, while custom `As` methods retain Go's destination-update semantics. Matcher failures identify the matcher call's location; cleanup, reporters, bail, and exact failed-case reruns use the normal suite behavior.

These matchers catch Kinmokusei `throw` signals, including assertion failures and runtime skip signals just as `expectThrows` does. Native Go panics propagate after cleanup. Returning an `error` value is not a throw: inspect such values with `expectErrorValue`. Wrap value-returning functions in a `(): void => { ... }` callback when testing their throws.

Vasest's matcher writing style is inspired by [Jest](https://jestjs.io/docs/expect). The implementation is independently written for Kinmokusei.

The current assertions are `expectEqual`, `expectNotEqual`, `expectTrue`, `expectFalse`, `expectError`, `expectNoError`, `expectErrorIs`, `expectErrorAs`, `expectContains`, `expectNotContains`, `expectMatches`, `expectNotMatches`, `expectThrows`, `expectThrowsMessage`, and `expectApprox`. Equality uses Go's deep equality rules, so a nil collection differs from an empty collection. When strings differ, `expectEqual` reports the first differing line and both values on that line. Regular expressions use Go's RE2 syntax; an invalid pattern fails the assertion. `expectApprox(actual, expected, tolerance)` accepts finite floating-point values within an inclusive, finite, non-negative absolute tolerance. Use `fail("message")` to fail a test explicitly; an empty message becomes `test failed`.

For string snapshots, call `expectSnapshot("name", renderedText)` inside a test. Vasest compares the text with a file under `__snapshots__/<test-file-name>/` next to the test file. Each file starts with the suite, case, and snapshot names for inspection. A missing snapshot fails; a changed snapshot reports the first differing line and its expected and actual values. Run `vasest --update-snapshots` to create or refresh snapshots for selected tests, then review the resulting files. Unchanged snapshots are left alone. Snapshot assertions require the Vasest runner, which supplies the test-file path.

Snapshots inside a test case are keyed by suite name, case name, and snapshot name, so different cases can reuse the same snapshot name. Snapshots created with the earlier name-only format are not read from a test case. Run `vasest --update-snapshots` to create scoped replacements, review them, and remove the unused old files. Direct calls outside a running case still use the name-only format.

For JSON-serializable values, use `expectJSONSnapshot("name", value)`. It writes indented JSON with a final newline and uses the same snapshot files and update flag. Go's JSON encoder sorts map keys, so map insertion order does not change the snapshot. Struct fields must be public to appear in the JSON; unsupported values fail with an encoding error.

`expectThrows((): void => { ... })` returns the thrown error so the test can inspect it. `expectThrowsMessage(body, "fragment")` requires a non-empty substring of its message. These assertions catch Kinmokusei `throw` errors, not uncaught Go panics. `runSuiteAndExit` is a convenience wrapper; direct `new Suite(...)` and `suite.run()`/`suite.runAndExit()` remain available when the summary is needed. The runner also reads suite outcomes, so a failing `suite.run()` fails its file even if `main` returns normally. A file that returns without running any suite fails with `no test suites ran`.

`expectErrorIs(actual, target)` verifies an error's cause with Go's `errors.Is` rules, following wrapping, joined errors, and custom `Is` methods. Errors with identical messages can still be different causes. `expectErrorIs(nil, nil)` succeeds; one nil argument and one non-nil argument fail.

`expectErrorAs(actual, &target)` finds an error assignable to the target's type using Go's `errors.As` rules and assigns it to the target. This supports wrapped and joined errors, custom `As` methods, native Go error types, and application classes implementing `error`:

```ts
import { expectErrorAs, expectErrorIs, expectEqual } from "vasest";
import go fmt from "fmt";
import go os from "os";

const [contents, readError] = os.ReadFile("missing-file");
const wrapped = fmt.Errorf("load configuration: %w", readError);
expectErrorIs(wrapped, os.ErrNotExist);
let pathError: *os.PathError = nil;
expectErrorAs(wrapped, &pathError);
expectEqual(pathError.Path, "missing-file");
```

Put these statements inside a test body. The destination must be a non-nil pointer whose element implements `error` or is an interface. Initialize a native pointer error variable with `nil`, then pass its address; for an application error class, declare a class variable and pass its address. An invalid destination fails the assertion before attempting a match, including when the actual error is nil. A missing match fails. Ordinary extraction leaves the destination unchanged on a mismatch; application-defined `As` methods receive the destination directly and can modify it even when returning false, following Go's behavior. A matching typed nil error assigns nil and succeeds; check the extracted value before dereferencing if the application permits typed nil errors. Errors caught by `expectThrows` and annotated with `withTestLocation` retain their original cause and type for these assertions. Mismatches record the assertion's location and ordinary body failure; cleanup and later selected tests follow the usual suite rules. Native panics in application-defined `Is`, `As`, or `Unwrap` methods still propagate.

For tests that need a disposable directory, `withTempDir((path: string): void => { ... })` creates one and removes it after the callback. Cleanup runs on normal return, Kinmokusei `throw`, and a propagating Go panic. The helper also reports a cleanup error instead of silently ignoring it. It does not cover a callback that terminates the process with `os.Exit`.

`withEnv("NAME", "value", (): void => { ... })` temporarily sets an environment variable and restores its previous value, including an unset state. Nested calls, thrown errors, and propagating Go panics restore the prior value. An invalid name or nil callback fails before the body runs. As with `withTempDir`, process termination prevents cleanup.

For resources that should remain available through `afterEach`, use `suite.testWithContext(name, body)` and register cleanup when the resource is acquired:

```ts
import { Suite, TestContext, expectNoError, runSuiteAndExit } from "vasest";
import go os from "os";

function main(): void {
  runSuiteAndExit("resources", (suite: Suite): void => {
    suite.testWithContext("uses a directory", (context: TestContext): void => {
      const [path, createError] = os.MkdirTemp("", "vasest-test-");
      expectNoError(createError);
      context.cleanup((): void => {
        expectNoError(os.RemoveAll(path));
      });
      // Use path here or pass context to a reusable fixture helper.
    });
  });
}
```

Each runnable case receives a fresh context. Cleanup runs after `afterEach`, in reverse registration order, before the case result and duration are recorded. Cleanup can register more work, which runs before older pending callbacks. Thrown cleanup errors are collected with body and hook errors; later callbacks still run, and an empty-message error still fails the case. A cleanup failure participates in `--bail`. Native Go panics still propagate after remaining cleanup callbacks and suite hooks run; a later panic during cleanup can replace an earlier panic.

Use `suite.beforeEachWithContext(body)` and `suite.afterEachWithContext(body)` to prepare or inspect resources for every selected runnable case. Both receive the same `TestContext` as that case's body and per-case fixture setup. Context hooks also work with ordinary `test` bodies and table rows:

```ts
import { Suite, TestContext, expectString, expectNoError, runSuiteAndExit } from "vasest";
import go os from "os";

function main(): void {
  runSuiteAndExit("resource hooks", (suite: Suite): void => {
    suite.beforeEachWithContext((context: TestContext): void => {
      context.setEnv("VASEST_TEST_DATA", context.tempDir());
    });
    suite.afterEachWithContext((context: TestContext): void => {
      const [info, statError] = os.Stat(os.Getenv("VASEST_TEST_DATA"));
      expectNoError(statError);
      // Cleanup registered here runs before earlier body/setup cleanup.
    });
    suite.test("uses prepared data", (): void => {
      expectString(os.Getenv("VASEST_TEST_DATA")).not.toHaveLength(0);
    });
  });
}
```

The before hook runs before per-case fixture setup. The after hook runs after the body or a failed/skipped before hook or fixture setup, while acquired resources are still available. Both may register cleanup; it runs after the after hook in reverse registration order. Before-hook skips skip the body, while an after-hook skip is a failure. Errors retain `beforeEach`, `afterEach` and `cleanup` phases and attached locations. Native panics still unwind after hooks and cleanup. Filtered, skipped, todo and bailed cases run neither hook, and reruns receive fresh contexts.

For each phase, the ordinary and context variants replace one another: the last valid registration wins. Nil callbacks are rejected without changing the previously registered hook.

Contexts belong to the current test's synchronous lifecycle. Registration after cleanup completes and nil callbacks are rejected. Filtered, skipped, todo, and bailed cases run no context body. Process exit or a forced runner timeout prevents cleanup, as with other fixtures.

`context.tempDir()` returns a new empty directory on each call and registers its recursive removal at case cleanup. It stays available through `afterEach`, and creation or cleanup errors fail the test. `context.setEnv("NAME", "value")` sets an environment variable until case cleanup, restoring its previous value or unset state. Repeated calls for the same variable restore in reverse order. Both helpers can be used from reusable functions that accept `TestContext`.

For example, `const path = context.tempDir(); context.setEnv("VASEST_TEST_DATA", path);` gives a test and its cleanup hooks a temporary data directory. Custom cleanup registered afterward runs before the directory is removed and the environment is restored. A closed context rejects these helpers before allocating a directory or changing an environment variable.

Use `waitFor(body, timeout, interval)` to retry an assertion while waiting for asynchronous work. For example, with `import go time from "time";`, `waitFor((): void => { expectEqual(readStatus(), "ready"); }, 2 * time.Second, 20 * time.Millisecond);` runs the callback immediately, then retries ordinary thrown Kinmokusei errors until it succeeds or the timeout expires. A timeout reports the last error and the attempt count. Timeout and interval must both be positive. Runtime skips propagate immediately, retaining their reason and error identity; native panics also propagate with normal cleanup. Waiting is clipped to the remaining timeout, and an expired wait does not start another attempt. A callback that blocks is allowed to finish; use the runner's per-file `--timeout` for an execution limit.

Use `waitForValue<T>(read, predicate, timeout, interval)` when the successful attempt should return a typed value. It reads once per attempt, passes the native value to the typed predicate once, and returns that same value on `true`. An ordinary read throw skips the predicate and retries; a false predicate or an ordinary predicate throw also retries. Explicit type arguments and typed callback bindings work without inference:

```ts
import { expect, waitForValue } from "vasest";
import go time from "time";

type UserID = distinct int64;

// Inside a test body:
let attempts = 0;
const read: () => UserID = (): UserID => {
  attempts++;
  return UserID(attempts);
};
const ready: (value: UserID) => boolean = (value: UserID): boolean => {
  return value == UserID(3);
};
const result: UserID = waitForValue<UserID>(read, ready, time.Second, time.Millisecond);
expect<UserID>(result).toEqual(UserID(3));
```

Inference is also available with `waitForValue(read, ready, timeout, interval)`. The compiler rejects incompatible reader results, predicate parameters, nominal types, numeric widths, predicate return types and timing arguments. Nil callbacks and non-positive timing arguments fail at the waiter's call site before executing either callback. Values follow native value and reference semantics, preserving class/map/slice references, mutations through those references, collection backing storage and nil-versus-empty distinctions; returned error values are ordinary values of `T`.

Each retry performs a fresh read. A timeout preserves the last callback error as its cause and retains its attached failure location, falling back to the `waitForValue` call site for an unlocated error. A false predicate reports `waitForValue predicate returned false` at that call site. Both helpers stop immediately for direct or ordinarily wrapped runtime skips; joined errors remain failures. Read/predicate callbacks are allowed to finish and are not interrupted by these helpers. Registered cleanup, reporters, bail and failed-case history follow the normal suite rules.

Use `suite.testEach(values, nameOf, body)` for a test matrix. For example, with `import go fmt from "fmt";`, `suite.testEach([0, 1, 2], (value: int): string => { return fmt.Sprintf("value %d", value); }, (value: int): void => { expectTrue(value >= 0); });` registers three independent tests. Each row has its own name, hooks, result, and `--filter` selection. Empty tables, blank or duplicate names, and nil callbacks are rejected before any row is registered.

Use `suite.testEachIndexed<T>(values, nameOf, body)` when repeated values need distinct names or a check needs the original row number. Both callbacks receive `(value, index)`, where `index` is a zero-based `int` fixed during registration. Filtering, shuffling and failed-case reruns preserve that original index:

```ts
import { Suite, expect, runSuiteAndExit } from "vasest";
import go fmt from "fmt";

type UserID = distinct int64;

function main(): void {
  runSuiteAndExit("repeated users", (suite: Suite): void => {
    const rows: UserID[] = [UserID(42), UserID(42)];
    const nameOf: (value: UserID, index: int) => string = (value: UserID, index: int): string => {
      return fmt.Sprintf("user row %d", index);
    };
    suite.testEachIndexed<UserID>(rows, nameOf, (value: UserID, index: int): void => {
      expect(value).toEqual(UserID(42));
      expect(index >= 0 && index < 2).toBe(true);
    });
  });
}
```

Type inference is optional: explicit generic arguments and typed callback bindings retain the row's native type, including nominal types and numeric widths. Indexed tables capture each row's value and index before calling `nameOf`. Later replacement of an element in the original table does not replace the captured row; class, map and slice references still follow native reference semantics, including mutations through those references. Invalid names or a naming callback failure prevent the batch from registering any rows. Registrations made explicitly inside a naming callback remain ordinary registrations.

`suite.testEachWithContext(values, nameOf, body)` supplies `body(value, context)` for each row, with a fresh `TestContext` and the same registration checks. Each row's fixtures and cleanup finish before the next selected row starts:

```ts
import { Suite, TestContext, expectEqual, runSuiteAndExit } from "vasest";
import go os from "os";

function main(): void {
  runSuiteAndExit("input modes", (suite: Suite): void => {
    suite.testEachWithContext(["small", "large"], (mode: string): string => {
      return mode;
    }, (mode: string, context: TestContext): void => {
      const path = context.tempDir();
      context.setEnv("VASEST_INPUT_MODE", mode);
      context.setEnv("VASEST_DATA_DIR", path);
      expectEqual(os.Getenv("VASEST_INPUT_MODE"), mode);
      expectEqual(os.Getenv("VASEST_DATA_DIR"), path);
    });
  });
}
```

Filtering and shuffling select or reorder whole rows. A body or cleanup failure fails that row; remaining rows continue unless `--bail` is set. Selected rows still run their cleanup before bail takes effect.

For reusable setup that returns a typed value, use `suite.testWithFixture(name, setup, body)`. The setup receives the case's `TestContext`; the body receives its returned value and the same context:

```ts
import { Suite, TestContext, expectEqual, runSuiteAndExit } from "vasest";
import go os from "os";

function dataDirectory(context: TestContext): string {
  const path = context.tempDir();
  context.setEnv("VASEST_DATA_DIR", path);
  return path;
}

function main(): void {
  runSuiteAndExit("data", (suite: Suite): void => {
    suite.testWithFixture("uses prepared data", dataDirectory, (path: string, context: TestContext): void => {
      expectEqual(os.Getenv("VASEST_DATA_DIR"), path);
    });
  });
}
```

Setup runs once per selected runnable case, after `beforeEach` and before its body. Every case and suite rerun prepares a fresh value; registration, filtering, bail, and failed `beforeAll` or `beforeEach` do not invoke setup for cases that will not run. Resources registered on the context stay available through `afterEach`, then cleanup runs in reverse order. Register cleanup immediately when acquiring a resource so a later setup error still releases it. Setup errors skip the body and appear with phase `fixture`; `afterEach` and registered cleanup still run. Native Go panics propagate after cleanup and suite hooks. Case duration includes setup.

For a table whose fixture depends on its row, use `suite.testEachWithFixture(values, nameOf, setup, body)`. The setup has signature `(value, context) => fixture`, and the body has signature `(value, fixture, context) => void`; row and fixture types are inferred independently. Each selected row prepares its own fixture. The same table registration checks, filtering, shuffling, and bail rules apply as for `testEachWithContext`. Nil setup or body callbacks are rejected during registration.

The indexed variants add the original index immediately after the row value: `testEachIndexedWithContext<T>` calls `body(value, index, context)`, and `testEachIndexedWithFixture<T, F>` calls `setup(value, index, context)` and `body(value, index, fixture, context)`. Both use `nameOf(value, index)` and the same atomic name validation, fresh contexts, runtime skips, failure phases and cleanup rules as their unindexed counterparts.

For resources shared by every selected case in a suite, use `FixtureSuite<T>`. Its setup runs once, and its typed value is passed to cases and hooks:

```ts
import { FixtureSuite, TestContext, expectNoError } from "vasest";
import go os from "os";

function main(): void {
  const suite = new FixtureSuite<string>("shared data", (context: TestContext): string => {
    return context.tempDir();
  });
  suite.test("uses the shared directory", (path: string, context: TestContext): void => {
    const [info, statError] = os.Stat(path);
    expectNoError(statError);
    // Use context.tempDir() for a separate directory owned by this case.
  });
  suite.runAndExit();
}
```

`FixtureSuite` supports `test`, `testEach`, `testEachIndexed`, `skip`, `skipEach`, `skipEachIndexed`, `todo`, `todoEach`, `todoEachIndexed`, `beforeAll`, `afterAll`, `beforeEach`, `afterEach`, `run`, `runTo`, and `runAndExit`. A case body receives `(fixture, context)`, and a table body receives `(row, fixture, context)` with independent row and fixture types. `testEachIndexed<R>` uses `nameOf(row, index)` and `body(row, index, fixture, context)`, preserving the original index and native row type. Each case still has its own context and cleanup. All four hooks receive the shared fixture. Setup runs before the typed `beforeAll` hook; suite cleanup runs after the typed `afterAll` hook, after all case cleanup. The suite's setup context supports `cleanup`, `tempDir`, and `setEnv`, with the same reverse cleanup order and error handling as a case context.

`FixtureSuite<T>` also supports `beforeEachWithContext((fixture: T, context: TestContext): void => { ... })` and `afterEachWithContext` with the same signature. They receive the shared fixture in its native type and the current case's context, with the same lifetime, replacement and failure rules as ordinary suite context hooks. The case context is distinct from the shared setup context; resources acquired through it are cleaned after that case.

A suite containing no selected runnable cases allocates no shared fixture and runs no hooks. A setup failure is reported as `<beforeAll>` with phase `beforeAll`, skips selected runnable bodies, and still releases resources already registered on the setup context. Typed hooks are not called without a returned value. If setup returns successfully but the typed `beforeAll` hook fails, the typed `afterAll` hook still runs. A nil returned fixture value is valid when its type permits it. Nil callbacks, blank or duplicate names, and invalid tables are rejected during registration.

Body failures, bail, and native Go panics still unwind shared cleanup. Errors from `afterAll` and shared cleanup are collected into one `<afterAll>` failure result, preserving `afterAll` and `cleanup` phases, including empty error messages. Suite duration includes shared setup and cleanup; individual case durations exclude them. Every rerun prepares a fresh fixture, and recursive runs of the same `FixtureSuite` are rejected. Suites and contexts have synchronous lifetimes and are not intended for concurrent use. Values retain their normal value/reference semantics; mutable objects and collections shared between cases are not cloned. Shared fixtures remain local to their suite's process when files run with `--jobs`.

Ordinary `Suite` also supports `beforeAllWithContext((context: TestContext): void => { ... })` to register shared cleanup without a typed returned value. Its resources remain available through `afterAll`, and partial setup failures still run registered cleanup. `beforeAll` and `beforeAllWithContext` replace one another; the last registration wins. Filtered, skipped, and todo-only suites run neither shared setup nor cleanup.

Use `MockFn<Argument, Return>` to replace a dependency that takes one typed argument and returns a value:

```ts
import { Suite, MockFn, expectEqual, runSuiteAndExit } from "vasest";

function main(): void {
  runSuiteAndExit("loading", (suite: Suite): void => {
    suite.test("uses the dependency", (): void => {
      const load = new MockFn<string, string>((key: string): string => {
        return "default:" + key;
      });
      load.returnValueOnce("cached");
      const dependency = load.invoke;
      expectEqual(dependency("first"), "cached");
      expectEqual(dependency("second"), "default:second");
      load.expectCalledTimes(2);
      load.expectCalledWith("second");
      load.expectNthCalledWith(1, "first");
      load.expectReturnedTimes(2);
      load.expectNthReturnedWith(1, "cached");
    });
  });
}
```

`implement(body)` and `returnValue(value)` replace the default behavior. `implementOnce(body)` and `returnValueOnce(value)` queue behavior in registration order; after the queue is consumed, calls use the current default. Nil implementations are rejected. Each call is recorded before its implementation runs, including calls that throw or panic, and a one-shot implementation is consumed even when it fails. `calls()` returns a copy of the history slice, `callCount()` returns its length, and `clearCalls()` clears the argument and result histories, preserving both default and queued behavior.

Use `throwError(exception)` to set a throwing default, or `throwErrorOnce(exception)` to append one throwing invocation to the queue. Both `MockFn<A, R>` and `MockAction<A>` accept native errors and application classes implementing `error`; mock argument and return types stay unchanged:

```ts
import { MockFn, MockAction, expectFunction } from "vasest";
import go errors from "errors";

// Inside a test body:
const unavailable = errors.New("service unavailable");
const load = new MockFn<string, string>((key: string): string => { return "default:" + key; });
load.returnValueOnce("cached");
load.throwErrorOnce(unavailable);
load.throwError(unavailable);
load.invoke("first"); // Returns "cached".
expectFunction((): void => { load.invoke("second"); }).toThrowCause(unavailable);
expectFunction((): void => { load.invoke("third"); }).toThrowCause(unavailable);

const notify = new MockAction<string>((message: string): void => {});
const queueError: (exception: error) => void = notify.throwErrorOnce;
queueError(unavailable);
expectFunction((): void => { notify.invoke("saved"); }).toThrowCause(unavailable);
notify.invoke("retry"); // Uses the original void default.
```

Configuration does not invoke the mock, inspect the error or alter existing history. Errors retain their concrete type, native reference and wrapping/joined causes; mutations to an application error remain visible when thrown. Nil and typed nil error payloads are allowed and still produce a `throw` outcome. `throwError` preserves existing one-shot behavior, while `throwErrorOnce` joins the same FIFO queue as `implementOnce` and `returnValueOnce`. A queued throw is consumed even when it skips or fails the surrounding test. `implement`/`returnValue` can replace a throwing default, `clearCalls` retains it and the queue, and `reset` discards both. Active calls finish their captured implementation; later calls use the newly configured behavior. Setter operations remain synchronized and can run inside an implementation. Returning an error through `returnValue` remains an ordinary return. Configured skip signals, attached locations and native error-formatting panics retain normal suite behavior and cleanup; plain errors receive no invented assertion location.

Use `reset(implementation)` when a reused mock needs fresh behavior and history. It replaces the default implementation, discards all unused one-shot implementations, and clears call, result and outcome histories in one operation. The replacement callback keeps the mock's exact argument and return types and is not executed by reset. Pass the new default explicitly; typed callback bindings work without inference:

```ts
import { MockFn } from "vasest";

type UserID = distinct int64;

// Inside a suite registration callback:
const load = new MockFn<UserID, string>((id: UserID): string => { return "initial"; });
const defaultLoad: (id: UserID) => string = (id: UserID): string => { return "loaded"; };
suite.beforeEach((): void => {
  load.reset(defaultLoad);
});
```

Nil replacement callbacks are rejected before changing any state. Previously returned history snapshots remain unchanged, while saved expectation objects read the new current history. Calls already in progress finish their captured implementation normally, including its native return, throw, runtime skip or panic; their eventual outcomes stay discarded from the reset history. Reset may run inside an implementation and remains synchronized with invocation, queue updates and history reads. Calls started after reset use the new default or any one-shot behavior queued afterward. Returning a native error value remains an ordinary return.

Call expectations use deep equality, and `expectNthCalledWith` uses indices starting at 1. For several arguments, use a typed request struct and an adapter callback. Arguments and returned values are retained with their normal value/reference semantics; objects and nested collections are not deep-copied. Mock state is synchronized for concurrent use, but callbacks run outside the lock and must protect their own shared state. Concurrent calls are recorded and consume queued behavior in lock-acquisition order.

`results()` returns a snapshot of the outcomes in invocation order, even when calls finish in another order. Each `MockResult<Return>` has a `status`: `incomplete` while the callback runs, `return` after normal completion, `throw` after a Kinmokusei exception, or `panic` after a native Go panic. Read `value` for a `return` result and `exception` for a `throw` result. Other values are zero/nil. The original exception, including its concrete type, or panic still propagates. Clearing history during an active call also discards that call's eventual outcome.

Use `expectReturnedTimes(count)` and `expectReturnedWith(value)` to check normal returns, and `expectNthReturnedWith(index, value)` to check a specific invocation. The index refers to the call, including calls that threw or are still running. `expectThrownTimes(count)` counts Kinmokusei throws, and `expectNthThrownMessage(index, fragment)` checks a non-empty substring of the error from a specific call. Pending calls and native panics do not count as returns or throws.

For a callback with no return value, use `MockAction<Argument>`. Its `invoke` method has type `(argument: Argument) => void`, so it can be passed directly to notification or event-handler dependencies:

```ts
import { Suite, MockAction, runSuiteAndExit } from "vasest";

function main(): void {
  runSuiteAndExit("notifications", (suite: Suite): void => {
    suite.test("sends the saved event", (): void => {
      const notify = new MockAction<string>((message: string): void => {});
      const send: (message: string) => void = notify.invoke;
      send("saved");
      notify.expectCalledTimes(1);
      notify.expectCalledWith("saved");
      notify.expectReturnedTimes(1);
    });
  });
}
```

`MockAction` supports `implement`, `implementOnce`, `throwError`, `throwErrorOnce`, `calls`, `callCount`, `clearCalls`, `reset`, and the same three call expectations as `MockFn`. `reset` accepts an `(argument: A) => void` replacement with the same clearing, validation and active-call rules as function mocks. Its `results()` returns `MockActionResult` entries with `status` and `exception`; a `return` status means the void callback completed normally. It also supports `expectReturnedTimes`, `expectThrownTimes`, and `expectNthThrownMessage`, following the same queue, outcome, history-copy, and concurrency rules.

For fluent call checks, use `expectMock<Argument>(mock)` with either `MockFn<Argument, Return>` or `MockAction<Argument>`. It provides `toHaveBeenCalled()`, `toHaveBeenCalledTimes(count)`, `toHaveBeenCalledWith(argument)`, `toHaveBeenNthCalledWith(index, argument)`, and `toHaveBeenLastCalledWith(argument)`, all with `.not`:

```ts
import { MockFn, MockAction, MockExpectation, expectMock } from "vasest";

type UserID = distinct int64;

// Inside a test body:
const id = UserID(42);
const load = new MockFn<UserID, string>((value: UserID): string => { return "loaded"; });
const matcher: MockExpectation<UserID> = expectMock<UserID>(load);
matcher.not.toHaveBeenCalled();
load.invoke(id);
matcher.toHaveBeenCalledTimes(1);
matcher.toHaveBeenNthCalledWith(1, id);
matcher.toHaveBeenLastCalledWith(id);
expectMock(load).not.toHaveBeenCalledWith(UserID(43));

const notify = new MockAction<UserID>((value: UserID): void => {});
notify.invoke(id);
new MockExpectation<UserID>(notify).toHaveBeenCalledWith(id);
```

The argument type is inferred or supplied explicitly; direct `MockExpectation<Argument>` construction also works. Argument checks preserve native assignability, numeric widths and nominal types, and use deep equality for nested values. Each matcher reads one current call-history snapshot without invoking the mock or consuming queued behavior. Saved matchers observe later calls and `clearCalls()`. Calls are counted when recorded, including callbacks that are still running, threw, or panicked; the matcher does not inspect completion status.

Counts must be non-negative `int` values and invocation indices start at 1. Invalid counts, indices, and nil sources fail even when negated, including a typed nil mock stored in an interface. A missing Nth or last call fails the positive argument check and passes its negated form. Nth and last checks compare the selected invocation's argument. Matcher failures identify the fluent call's location and participate in ordinary cleanup, reporting, bail, and failure-history reruns.

Both mock classes implement the exported `MockCallSource<Argument>` interface, whose `calls(): Argument[]` method also lets a typed custom spy provide its history. Custom sources must supply a stable history snapshot and protect any shared state themselves. Errors or native panics from their `calls()` method propagate unchanged, including an attached failure location, and cleanup still runs.

For domain-specific argument checks, use `toHaveBeenCalledSatisfying(predicate)`, `toHaveBeenNthCalledSatisfying(index, predicate)`, or `toHaveBeenLastCalledSatisfying(predicate)`. The predicate takes the exact argument type `A` and returns `boolean`; all three support `.not`. Inference, explicit generic arguments and `MockExpectation<A>` constructors work:

```ts
import { MockAction, MockExpectation, expectMock } from "vasest";

type UserID = distinct int64;

// Inside a test body:
const notify = new MockAction<UserID>((value: UserID): void => {});
notify.invoke(UserID(42));
const isPositive: (value: UserID) => boolean = (value: UserID): boolean => {
  return value > UserID(0);
};
expectMock<UserID>(notify).toHaveBeenCalledSatisfying(isPositive);
expectMock(notify).toHaveBeenNthCalledSatisfying(1, isPositive);
new MockExpectation<UserID>(notify).not.toHaveBeenLastCalledSatisfying((value: UserID): boolean => {
  return value < UserID(0);
});
```

The first matcher requires at least one recorded argument to match, visiting calls in invocation order and stopping at the first match. Its negation requires no matching argument. Nth and last matchers evaluate only the selected invocation, once. Missing calls fail positive checks and pass negated checks without invoking the predicate. All completion states participate, including running, throwing and panicking calls. Nil predicates and indices below 1 fail both forms before reading history; nil or typed nil sources fail even when negated.

Each check reads one current snapshot and preserves native argument types and reference semantics. Predicates may add or clear calls; those changes affect subsequent checks, while the current check keeps its selected snapshot. Native type checking rejects incompatible callback parameters, numeric widths, nominal types and return types. Callback throws, runtime skips, attached failure locations and native panics propagate unchanged with normal cleanup, as do errors from custom call sources. Matcher mismatches and invalid arguments retain the fluent call's location; the any-call mismatch includes how many arguments were evaluated.

For typed function-mock returns, use `expectMockReturn<Return>(mock)` with `MockFn<Argument, Return>`. It provides `toHaveReturned()`, `toHaveReturnedTimes(count)`, `toHaveReturnedWith(value)`, `toHaveNthReturnedWith(index, value)`, and `toHaveLastReturnedWith(value)`, each supporting `.not`:

```ts
import { MockFn, MockReturnExpectation, expectMockReturn } from "vasest";

type UserID = distinct int64;

// Inside a test body:
const load = new MockFn<string, UserID>((key: string): UserID => { return UserID(42); });
const matcher: MockReturnExpectation<UserID> = expectMockReturn<UserID>(load);
matcher.not.toHaveReturned();
load.invoke("first");
matcher.toHaveReturnedTimes(1);
matcher.toHaveReturnedWith(UserID(42));
new MockReturnExpectation<UserID>(load).toHaveNthReturnedWith(1, UserID(42));
expectMockReturn(load).toHaveLastReturnedWith(UserID(42));
```

The return type can be inferred, supplied explicitly, or stated through the constructor. Expected values must be assignable to that native return type, preserving nominal types and numeric widths. Value comparisons use deep equality, including class contents and nil-versus-empty collections. A matcher reads one current result snapshot without invoking the mock or consuming queued implementations. Saved matchers observe later returns and `clearCalls()`, and retain normal reference semantics for nested returned values.

Only results with status `return` count or match a returned value. Calls that are still running, threw, or panicked do not match their zero/nil placeholder values. The Nth index and last position refer to invocation order, including all result statuses, even when calls finish in another order. A missing or non-returning selected invocation fails a positive value check and passes its negated form; the last matcher checks the last invocation's result. Counts must be non-negative `int` values and indices start at 1. Invalid counts, indices, and nil or typed nil sources fail in both forms. Failure diagnostics distinguish absent invocations, non-return statuses, and wrong values, and preserve the matcher call's location through reporting and failed-case reruns.

`MockFn` implements `MockReturnSource<Return>`, whose `results(): MockResult<Return>[]` method also supports typed custom result sources. They must provide stable snapshots and synchronize shared state. Errors and native panics from the source propagate unchanged with normal cleanup, including any failure location attached by the source.

Return-value predicates use `toHaveReturnedSatisfying(predicate)`, `toHaveNthReturnedSatisfying(index, predicate)`, and `toHaveLastReturnedSatisfying(predicate)`, each with `.not`. The predicate takes the exact native return type `R` and returns `boolean`; inferred calls, explicit type arguments and `MockReturnExpectation<R>` constructors work:

```ts
import { MockFn, MockReturnExpectation, expectMockReturn } from "vasest";

type UserID = distinct int64;

// Inside a test body:
const load = new MockFn<string, UserID>((key: string): UserID => { return UserID(42); });
load.invoke("first");
const isPositive: (value: UserID) => boolean = (value: UserID): boolean => {
  return value > UserID(0);
};
expectMockReturn<UserID>(load).toHaveReturnedSatisfying(isPositive);
expectMockReturn(load).toHaveNthReturnedSatisfying(1, isPositive);
new MockReturnExpectation<UserID>(load).not.toHaveLastReturnedSatisfying((value: UserID): boolean => {
  return value < UserID(0);
});
```

Only results with status `return` invoke the predicate, including genuine zero/nil values and normally returned errors. Throwing, running and panicking calls never pass their placeholder values to it. The any-return matcher scans in invocation order and stops at the first matching return; its negation requires no matching return. Nth and last select the invocation, including non-returning calls, and evaluate at most once. Missing or non-returning selected invocations fail positive checks and pass negation without running the predicate. The last matcher checks the last invocation rather than the last successful return. Failure diagnostics distinguish missing calls, non-return statuses and predicate mismatches; the any-return mismatch includes evaluated-return and total-call counts.

Nil predicates and invalid indices fail both forms before reading results; nil or typed nil sources fail even when negated. Each check uses one live result snapshot, preserving queued behavior and native value/reference semantics. Changes to mock history made by a predicate affect later checks, while the current check retains its selected snapshot. Callback and custom-source errors, runtime skips, attached locations and native panics propagate unchanged with normal cleanup. Predicate type mismatches are rejected by the compiler, and matcher failures identify the fluent call.

Use `expectMockOutcome(mock)` for completion and throw checks shared by `MockFn<Argument, Return>` and `MockAction<Argument>`. It provides `toHaveReturned()`, `toHaveReturnedTimes(count)`, `toHaveThrown()`, `toHaveThrownTimes(count)`, `toHaveNthThrownMessage(index, fragment)`, and `toHaveLastThrownMessage(fragment)`, all supporting `.not`. `new MockOutcomeExpectation(mock)` is also available:

```ts
import { MockAction, MockOutcomeExpectation, expectMockOutcome, expectFunction } from "vasest";
import go errors from "errors";

// Inside a test body:
const notify = new MockAction<string>((message: string): void => {});
notify.implementOnce((message: string): void => { throw errors.New("delivery failed"); });
expectFunction((): void => { notify.invoke("first"); }).toThrowMessage("failed");
expectMockOutcome(notify).toHaveThrownTimes(1);
expectMockOutcome(notify).toHaveNthThrownMessage(1, "delivery");
notify.invoke("second");
new MockOutcomeExpectation(notify).toHaveReturnedTimes(1);
expectMockOutcome(notify).not.toHaveLastThrownMessage("failed");
```

Normal void completion has status `return`; a function returning an error value also completes normally. A Kinmokusei throw has status `throw`, including a nil error payload. Pending calls and native panics are counted in neither category. Message matchers require a throwing selected invocation and a non-empty substring of its native error message. A nil error payload has an empty message. Nth and last refer to invocation order, including non-throwing calls, even when calls finish in another order. Missing, pending, normally completed, or panicking selected invocations fail the positive message matcher and pass its negated form. Negative counts, indices below 1, empty fragments, and nil or typed nil sources fail in both forms.

Use `toHaveNthThrownCause(index, cause)` and `toHaveLastThrownCause(cause)` to match native error chains with `errors.Is`. Type extraction uses `toHaveNthThrownType<T>(index, &found)` and `toHaveLastThrownType<T>(&found)` with `errors.As`. The destination can infer `T`, or you can state it explicitly:

```ts
import { MockAction, expectMockOutcome, expectFunction } from "vasest";
import go fmt from "fmt";
import go os from "os";

// Inside a test body:
const original = os.PathError { Op: "open", Path: "missing", Err: os.ErrNotExist };
const load = new MockAction<string>((path: string): void => {
  throw fmt.Errorf("load: %w", &original);
});
expectFunction((): void => { load.invoke("missing"); }).toThrow();
expectMockOutcome(load).toHaveNthThrownCause(1, os.ErrNotExist);
let found: *os.PathError = nil;
expectMockOutcome(load).toHaveLastThrownType<*os.PathError>(&found);
expectMockOutcome(load).toHaveNthThrownType(1, &found);
expectMockOutcome(load).not.toHaveLastThrownCause(os.ErrPermission);
```

Negated type checks take only the type, such as `.not.toHaveNthThrownType<*os.PathError>(2)` or `.not.toHaveLastThrownType<*os.PathError>()`, and use a private destination. Cause and type checks select invocations in the same order as message checks; missing or non-throwing invocations fail positive checks and pass negated ones. A nil throw matches a nil cause, while a typed nil exception retains its concrete type. A type destination must be non-nil and point to an error type or an interface; negation requires an error type or interface too. Invalid indices, types, destinations and nil sources fail before matching. Destination/type validation and index validation run before reading the source. Custom `Is` and `As` hooks retain native behavior, including destination updates made by `As` when it returns false; errors and native panics from those hooks propagate with normal cleanup.

For domain-specific error checks, use `toHaveThrownSatisfying(predicate)`, `toHaveNthThrownSatisfying(index, predicate)` or `toHaveLastThrownSatisfying(predicate)`, each with `.not`. The callback takes the original native `error` and returns `boolean`. Typed callback bindings and direct `MockOutcomeExpectation` construction are available without generic inference:

```ts
import { MockAction, MockOutcomeExpectation, expectMockOutcome, expectFunction } from "vasest";
import go errors from "errors";
import go fmt from "fmt";

// Inside a test body:
const unavailable = errors.New("service unavailable");
const send = new MockAction<string>((message: string): void => {
  throw fmt.Errorf("send: %w", unavailable);
});
expectFunction((): void => { send.invoke("金木犀"); }).toThrow();
const isUnavailable: (exception: error) => boolean = (exception: error): boolean => {
  return errors.Is(exception, unavailable);
};
expectMockOutcome(send).toHaveThrownSatisfying(isUnavailable);
new MockOutcomeExpectation(send).toHaveNthThrownSatisfying(1, isUnavailable);
expectMockOutcome(send).not.toHaveLastThrownSatisfying((exception: error): boolean => {
  return exception == nil;
});
```

The any-throw matcher visits throwing invocations in call order and stops at the first match; negation requires no matching throw. Nth and last evaluate only the selected invocation, once. Nil throw payloads are passed to the callback, and typed nil errors keep their native identity. Missing, returned, pending or panicking invocations fail positive selected-call checks and pass negated checks without invoking the callback. A normally returned error value is excluded. Matchers do not call `Error()`, `Is()` or `As()` themselves.

Nil predicates and indices below 1 fail before reading history, even when negated. Each check uses one outcome snapshot, so callback changes to history affect subsequent checks. Callback and source throws, runtime skips, attached locations and native panics propagate unchanged with normal cleanup. Mismatches retain the fluent caller's location; the any-throw mismatch reports how many throws were checked.

Use `toHaveThrownTypeSatisfying<T>(predicate)`, `toHaveNthThrownTypeSatisfying<T>(index, predicate)` or `toHaveLastThrownTypeSatisfying<T>(predicate)` to check a mock's extracted error type and contents together. Each supports `.not`, explicit type arguments, inference from a typed callback binding and direct outcome matcher constructors:

```ts
import { MockAction, MockOutcomeExpectation, expectMockOutcome, expectFunction } from "vasest";
import go os from "os";

// Inside a test body:
const original = os.PathError { Op: "open", Path: "missing", Err: os.ErrNotExist };
const load = new MockAction<string>((path: string): void => { throw &original; });
expectFunction((): void => { load.invoke("missing"); }).toThrow();
const isMissingPath: (value: *os.PathError) => boolean = (value: *os.PathError): boolean => {
  return value != nil && value.Path == "missing";
};
expectMockOutcome(load).toHaveThrownTypeSatisfying<*os.PathError>(isMissingPath);
new MockOutcomeExpectation(load).toHaveNthThrownTypeSatisfying(1, isMissingPath);
expectMockOutcome(load).not.toHaveLastThrownTypeSatisfying<*os.PathError>((value: *os.PathError): boolean => {
  return value != nil && value.Op == "write";
});
```

Only throwing invocations undergo native `errors.As` extraction, each with a fresh private destination. The predicate receives the first assignable value once when extraction succeeds. It can inspect error types or interfaces, including non-error interfaces and matching typed nil values, without losing native fields or references. A nil throw has no assignable error value. The any matcher visits throws in invocation order and stops at the first successful type-and-predicate match; a false predicate continues to the next invocation, without searching for another matching type within the same error chain. Its negation requires no matching throw. Nth and last select invocations, including non-throwing calls, and negate the combined type-and-predicate condition. Missing, returned, pending or panicking selections pass negation without extraction or callback execution.

Nil predicates, invalid types and indices below 1 fail before reading history, even when negated; nil or typed nil sources fail too. Each check reads one live outcome snapshot, preserves queued behavior and keeps that selected history when a predicate clears or invokes the mock. Later checks observe new history and completions. Mismatch diagnostics do not format the original error. Source, custom `As` and predicate throws, skips, attached locations and native panics propagate unchanged with normal cleanup; validation and mismatch failures identify the fluent call.

Each matcher reads one live outcome snapshot, preserving queued behavior and observing later completions and `clearCalls()`. Both mock classes expose `outcomes(): MockOutcomeResult[]`, a fresh metadata slice containing `status` and the original `exception: error`. Replacing an entry does not change stored history; error values retain native reference semantics. Typed argument and return APIs remain available through `expectMock<A>` and `expectMockReturn<R>`.

Both mocks implement `MockOutcomeSource`, which lets custom sources provide synchronized outcome snapshots. Source errors and native panics, including panics from an error's `Error()` method during message matching, propagate with normal cleanup. Matcher mismatches preserve the fluent call's location; failures thrown by a source retain their attached location.

For a temporarily disabled test, use `suite.skip("case name", (): void => { ... });`; its body and per-test hooks do not run. For a planned test without a body, use `suite.todo("case name");`. Both appear in the output and can be selected by `--filter`. By default, a suite containing only skipped or todo cases exits successfully without running suite hooks. `Summary.results` records each selected case's `status` as `pass`, `fail`, `skip`, or `todo`; `Summary.skipped` also includes cases excluded by a filter.

Use `skipEach<T>(values, nameOf, body)` to disable a whole typed table, or `todoEach<T>(values, nameOf)` to register planned rows without bodies. `skipEachIndexed<T>` and `todoEachIndexed<T>` also pass the original zero-based `int` index to naming callbacks; the skipped body receives the index too:

```ts
// During suite registration, with import go fmt from "fmt";
const rows: int64[] = [int64(42), int64(42)];
suite.skipEachIndexed<int64>(rows,
  (value: int64, index: int): string => { return fmt.Sprintf("disabled %d", index); },
  (value: int64, index: int): void => { expect(value).toEqual(int64(42)); });
suite.todoEachIndexed<int64>(rows,
  (value: int64, index: int): string => { return fmt.Sprintf("planned %d", index); });
```

All four methods also work on `FixtureSuite<T>`, with independently typed rows. Its skipped body accepts `(row, fixture, context)` or `(row, index, fixture, context)`, matching its runnable table methods. Explicit generic arguments and typed callbacks keep inference optional; incompatible row, index, fixture and numeric types are rejected by the compiler.

Names are evaluated once during registration, even for disabled rows. Nil callbacks, empty tables, blank names and duplicate names are rejected without registering any row from the batch, including collisions introduced by a naming callback. Exceptions and native panics from naming callbacks propagate unchanged. Indexed tables capture native row values and indices before naming, retaining reference semantics. Skipped bodies never run, and inactive rows acquire no fixtures or cleanup resources. An inactive-only selection runs no hooks or shared setup. Filtering, seeded shuffling, repeats, all reporters and failed-case reruns follow the usual skip/todo rules; `--fail-on-todo` selects exact planned row names for failure history.

Use `skipTest(reason)` when availability can only be checked during execution:

```ts
import { Suite, TestContext, skipTest } from "vasest";
import go os from "os";

function main(): void {
  const suite = new Suite("optional service");
  suite.testWithContext("connects to the service", (context: TestContext): void => {
    const path = context.tempDir();
    if (os.Getenv("SERVICE_URL") == "") {
      skipTest("SERVICE_URL is not configured");
    }
    // Use the service and path; the directory is removed even after a skip.
  });
  suite.runAndExit();
}
```

The reason must contain non-whitespace text. A skip stops the current callback by throwing a framework control signal. Avoid catching that signal unless intentionally intercepting it; `expectThrows` can catch it like other Kinmokusei throws. Use `skipTest` in a test body, `beforeEach`, or per-case fixture setup. `afterEach` and registered cleanup still run, and later selected cases continue even with `--bail`. A successfully skipped case has status `skip`, a reason in `TestResult.message`, no failure details, and a measured duration including setup and cleanup. A cleanup failure takes precedence and fails the case, participating in bail. Calling `skipTest` from `afterEach`, cleanup, or `afterAll` also fails that teardown phase. Native Go panics still propagate after cleanup.

A skip in `beforeAll`, `beforeAllWithContext`, or `FixtureSuite` shared setup skips all selected runnable cases with the same reason. Shared cleanup still releases acquired resources. An ordinary `afterAll` hook still runs; typed shared hooks run only if setup returned a value. Cases whose bodies never start have zero duration; shared setup and cleanup time belong to the suite. Static `skip` and `todo` registrations retain their own status, and selected todo cases still follow `--fail-on-todo`. Skipped cases count as successful filter/history matches, keep their reasons in JSON and JUnit, and are omitted from new failure histories unless teardown fails. Ordinary error wrapping and explicit locations preserve a skip signal; a joined error remains a failure so that a skipped branch cannot hide another error. Each repeat and watch run evaluates the condition again.

## Project configuration

Vasest automatically reads `vasest.config.json` in the current working directory. It does not search parent directories. For example:

```json
{
  "version": 1,
  "testPath": "tests",
  "jobs": 4,
  "timeout": "30s",
  "reporter": "text",
  "bail": false,
  "failOnTodo": true
}
```

The supported defaults are:

| Field | Value | Default when omitted |
| --- | --- | --- |
| `version` | Required integer `1` | Required |
| `testPath` | Non-empty test file or directory path | `tests` under the current working directory |
| `include` | Array of relative file patterns | All discovered test files |
| `exclude` | Array of relative file patterns | No additional exclusions |
| `jobs` | Positive integer | `1` |
| `repeat` | Positive integer number of iterations | `1` |
| `timeout` | Positive duration such as `30s` | No execution limit |
| `reporter` | `text`, `json`, or `junit` | `text` |
| `filter` | Non-empty, case-sensitive case-name substring | All cases |
| `bail` | Boolean | `false` |
| `failOnTodo` | Boolean | `false` |

Optional fields may be `null`, which has the same effect as omitting them. Relative `testPath` values resolve against the configuration file's directory; a test path supplied on the command line resolves against the current working directory. Run `vasest --config configs/ci.json` to use a different JSON configuration, or `vasest --no-config` to ignore automatic configuration. These two options cannot be combined. A missing automatic file uses ordinary defaults; a missing explicit file, malformed JSON, unknown fields, unsupported version, or invalid value returns status `2` before tests launch. The entire file is validated even when the command line overrides one of its values. Help and version output remain available with a broken configuration.

Command-line options override configured defaults. Use `--repeat 1`, `--jobs 1`, `--reporter json`, `--filter text`, or `--timeout 10s` to replace values; `--no-filter`, `--no-timeout`, `--no-bail`, and `--allow-todo` clear the corresponding defaults. For repeated scalar options, the last command-line value wins. For `include` and `exclude`, the first corresponding CLI option replaces that configured list, and subsequent occurrences append patterns. `--no-include` and `--no-exclude` clear their lists. `--list` uses the configured test path and file patterns and skips inherited execution settings, so a project with a configured filter, parallelism, or reporter can still list all discovered files. Execution settings explicitly passed with `--list` retain the ordinary argument restrictions. Other options, such as snapshots, shuffle, shards, coverage, artifacts, and failure-history files, are supplied on the command line.

Watch mode tracks the active configuration file, including an explicit file outside the project. Edits reload the test path, include/exclude patterns, jobs, repeat, timeout, filter, bail, and todo policy; newly selected external test directories are added to its scan roots. Creating an automatic configuration file also triggers a run. An invalid edit or deletion of an explicit configuration reports a status-`2` run and keeps watching, so fixing or recreating the file can recover the session. Deleting an automatic configuration restores ordinary defaults. The reporter is fixed when watch starts to keep one output protocol; restart watch to change it. Unrelated JSON files, including saved failure histories, do not trigger runs.

## Run tests

Build the runner from this repository:

```sh
keika build -o build/vasest cmd/vasest/main.km
```

Run it from the application project directory. By default, it scans `tests/` recursively:

```sh
vasest
vasest tests/arithmetic.test.km
vasest --list
vasest --config configs/ci.json
vasest --no-config
vasest --filter addition
vasest --include 'unit/**/*.test.km' --exclude '**/slow*.test.km'
vasest --list --include 'integration/**'
vasest --save-failures failures.json
vasest --rerun-failed failures.json --save-failures failures.json
vasest --bail
vasest --fail-on-todo
vasest --update-snapshots
vasest --timeout 30s
vasest --shuffle 42
vasest --shard 1/3
vasest --jobs 4
vasest --repeat 10 --jobs 4
vasest --coverage .coverage --jobs 4
vasest --artifacts .artifacts --coverage .coverage --jobs 4
vasest --watch
vasest --watch --jobs 4 --filter addition
vasest --watch --watch-interval 500ms
vasest --list --shard 1/3
vasest --reporter json
vasest --reporter junit > test-results.xml
```

A directory or one test file can be passed as the positional argument. Recursive discovery selects regular `*.test.km` files, including links to regular files. Broken links, links to directories, and special files such as named pipes or sockets are skipped; an explicitly selected special file returns status `2` before compilation. Hidden directories, `node_modules`, `vendor`, `build`, and `dist` are skipped during recursive discovery. `KEIKA` can select a different Kinmokusei executable.

`--filter text` runs only tests whose names contain `text` (case-sensitive), across all discovered files. Files with no matching tests are skipped; if no test matches anywhere, the command fails. `--bail` stops after the first failed test in a suite, marks remaining cases there as not run, and stops scheduling new files after a file fails. Cleanup hooks still run. `--fail-on-todo` makes a suite fail when one of its selected cases has `todo` status; filtered-out todo cases do not count. The runner reports passed, failed, and not-run file counts. `--list` cannot be combined with `--save-failures`, `--rerun-failed`, `--artifacts`, `--coverage`, `--filter`, `--bail`, `--fail-on-todo`, `--update-snapshots`, `--timeout`, `--shuffle`, `--jobs`, `--repeat`, `--watch`, or a machine-readable reporter. `beforeAll` and `afterAll` run once around a suite with selected tests, while `beforeEach` and `afterEach` run for each selected test. If `beforeAll` fails, selected tests are skipped and the suite fails; `afterAll` still runs. If per-test setup fails, that test body is skipped but its cleanup still runs. If both the body and cleanup fail, both error messages are reported. Uncaught Go panics still abort a file, but cleanup hooks run before the panic propagates.

`--include pattern` selects discovered test files that match any included pattern; an empty include list selects all discovered files. `--exclude pattern` removes files matching any exclusion, including files selected by an include pattern. Both flags can be repeated. Patterns are relative to the directory being searched, or to the containing directory when the input is one test file, and use `/` as the separator on every platform. A single-file input still obeys these patterns; use `--no-include --no-exclude` to clear inherited restrictions. Quote patterns in the shell so Vasest receives them intact.

| Pattern syntax | Meaning |
| --- | --- |
| `*` | Zero or more characters within one path component |
| `?` | One character within one path component |
| `[abc]`, `[0-9]`, `[^a]` | Character class, range, or negated class |
| `**` | Zero or more path components, used as a complete component |

For example, `*.test.km` selects only files directly under the search root, `**/*.test.km` also includes nested files, and `unit/**` selects files beneath `unit/`. Matching is case-sensitive. A leading `./` is accepted; backslash escapes literal pattern characters. Empty or absolute patterns, empty/dot/parent components, malformed character classes, and partial components such as `**name` return status `2`. These patterns can also be saved as `include` and `exclude` arrays in configuration. They always use the effective search directory as their base, including when a CLI test path overrides `testPath`.

Recursive discovery applies its ordinary hidden/generated-directory rules first. File patterns then filter that list, before sharding, failure-history selection, and shuffling. CI jobs must use the same pattern lists to partition the same eligible files. If discovered files exist but patterns select none, execution and list mode return status `1`; JSON reports `noMatch: true` and JUnit emits a `<files>` failure. Coverage and artifact manifests remain empty and an existing failure-history destination is preserved. A nonempty eligible set assigned to an empty shard still succeeds.

Text execution adds `FILES N selected, M excluded` before the run, describing pattern selection before sharding and failure-history selection. JSON summaries add `include`, `exclude`, and `excludedFiles`; `excludedFiles` counts removals by file patterns before sharding. List output stays one path per line. Watch retains its project source scan to track imported code, reloads configured patterns on changes, and can recover from an empty selection when a new matching file is created.

`--save-failures file.json` atomically saves a versioned JSON snapshot of failures from this invocation. The parent directory must already exist. Paths are relative to the history file's directory, so moving the project and history together preserves selection. Case failures, including per-case setup/cleanup failures and strict todo failures, retain exact suite and case names. Shared setup, teardown, or cleanup failures select the entire suite. Compilation errors, abnormal process exits, timeouts, and incomplete or malformed reports select the entire file. The framework records its own `runAndExit()` failure exit, so that ordinary assertion failures still select cases; older framework versions without that marker conservatively select the file.

`--rerun-failed file.json` intersects that history with files remaining after include/exclude selection under the requested test path. Sharding uses the pattern-filtered discovery order, then history selection runs before shuffling. Case selection uses exact names; when history selects individual suites or cases, unrelated suites run no hooks or fixtures. Combine it with `--filter`, `--jobs`, `--bail`, snapshots, timeout, coverage, artifacts, and any reporter. An empty history succeeds without launching tests when file selection leaves an eligible set or an empty assigned shard. A nonempty history with no matching file under the requested path, or no matching case in the selected files, fails with `noMatch`; an empty assigned shard succeeds. A shard with no recorded failures also succeeds when the history matches discovered files assigned to another shard. Renamed/deleted cases are not inferred from old names. If a failed suite name appears in multiple reports from the same file, including passing reports, saved history selects the whole file. This prevents a passing duplicate from hiding a renamed failure; unrelated passing duplicates preserve exact selection of a uniquely named failed suite.

If a suite name used by saved case selections appears in multiple reports during a rerun, the runner reports `ambiguous rerun suite` even when the selected cases passed. With `--save-failures`, it records a whole-file failure. The next rerun of that saved history selects the whole file, so a renamed original failure cannot be hidden by a passing duplicate. Whole-suite and whole-file selections already select all cases under their selected names before applying an optional case filter; unrelated duplicated suite names do not broaden case selections. Use unique suite names within a file for precise selection.

Saving and reading the same file is supported: a successful rerun replaces it with an empty failure list. The saved list covers failures observed in the current invocation only; cases excluded by file patterns, a case filter, or a shard, or not run because of bail, are omitted rather than merged with an earlier history. Use a separate output history when preserving a broader failure list. A no-match selection leaves the destination history untouched.

Configuration, failure history, suite-report JSON, and runtime rerun selections must use valid UTF-8 and paired Unicode surrogate escapes; malformed bytes and unpaired `\ud800`/`\udc00` escapes are rejected before JSON decoding can replace them. Valid surrogate pairs (such as emoji), literal backslash-u text, and the actual replacement character `�` retain their exact names. JSON object field names must be unique after escape decoding and case folding: `name`, `NAME`, and `na\u006de` refer to the same native field and cannot occur together in one object. Duplicate fields fail before tests or history changes; malformed suite reports fail their file while retaining earlier valid results and the original report artifact. Separate objects can reuse field names, and JSON text inside string values remains literal text.

Selected test-file paths must also use valid UTF-8, including their absolute location, and invalid paths fail before compilation or history changes. File patterns and sharding still exclude unrelated files before this validation. Invalid or unreadable histories and save errors return status `2`; a failed atomic replacement preserves the previous file. Text output prints `FAILURES`, JSON adds a `failures` event before its summary, and JUnit remains one XML document. `--watch` can save failures after each iteration; `--rerun-failed` cannot be combined with watch.

`--timeout duration` sets a positive execution limit for each test file. Vasest builds the file first, so compilation time is outside the limit. Durations use Go syntax such as `500ms`, `30s`, or `2m`. A file that exceeds the limit fails with a timeout message; the runner continues to other files unless `--bail` is set. The default is no timeout. A forced timeout kills the test process, so its cleanup hooks cannot run; subprocesses started by the test need their own cleanup.

After the test executable exits or is killed, captured output may drain for at most one additional timeout interval before the runner closes its capture pipes. A subprocess retaining stdout or stderr therefore cannot block the run indefinitely. A file whose output never closes fails with a timeout message even if its suite already reported success; failure history selects that entire file. Timeout mode captures output with every reporter; text output is displayed after each file finishes.

`--shuffle seed` runs files and selected tests in a shuffled order. The seed must be a non-negative integer, and Vasest prints it so a failure can be rerun with the same order. The same seed reproduces the order with the same Go toolchain. Filtering happens before case shuffling; `--bail` stops each suite at its first failure in that order. File scheduling follows the shuffled order. Without `--shuffle`, registration and discovery order are preserved.

`--shard index/count` partitions eligible discovered files between CI jobs, with indices starting at 1. Files are assigned round-robin in deterministic discovery order before shuffling, so the shards do not overlap and together cover every eligible file. Use the same checkout, input path, and include/exclude patterns in each job; adding or removing files can change the assignment. Combine it with `--list` to inspect a shard. A shard assigned no files succeeds, even with a name filter; a directory with no test files still fails. Each runner executes its assigned files sequentially by default; add `--jobs` to run them concurrently.

`--jobs count` runs up to a positive integer number of test files concurrently; the default is `1`. Each file runs in its own process, and cases within a file remain sequential. Compilation is serialized to protect the compiler's shared generated files, then execution overlaps with other files. With parallel execution, standard input is closed and output is captured separately for each file. All three reporters emit results after the workers finish, in discovery order or the seeded shuffled order, so output from different files does not interleave. A long-running file can delay the display of completed results; use `--timeout` to bound execution.

With `--jobs` and `--bail`, a failure stops new file scheduling once its process and suite reports have been checked. Files already assigned to workers finish normally, including cleanup; their results are included, and unassigned files count as not run. The number of files executed before bail can vary with timing. Shuffling reproduces file order but does not guarantee process completion order. Concurrent files need separate external resources, such as database names or network ports; temporary directories and environment variables managed by each test context remain local to its process.

`--repeat count` runs the selected files a positive integer number of times; the default is `1`. Each iteration launches fresh test processes and completes before the next starts. It can expose intermittent failures and external state left between executions. In-process variables and suite fixtures are recreated each time. File patterns, the shard, failure-history selection, and the shuffle seed stay fixed across the batch, so file and case order repeat with the same seed. Parallel files still have the usual timing differences. Tests receive `VASEST_REPEAT_INDEX` (starting at `1`) and `VASEST_REPEAT_COUNT`, including `1/1` for an ordinary run.

Any failed execution keeps the batch failed even if that file passes later. Without bail, later iterations continue after test, compile, report-validation, and timeout failures. `--bail` also stops subsequent iterations after the first failed iteration, after already assigned parallel files finish. A runner infrastructure error or an iteration with no matching tests stops the batch. Empty shards and empty failure histories launch no tests and are handled once. Reported file counts count executions across the requested batch; `notRunFiles` includes files in iterations omitted after a stop.

For counts above `1`, text adds `REPEAT N/count`; JSON adds a `repeat` event before each started iteration and `repeatIndex`/`repeatCount` to its suite, file, coverage, and artifact events. Its final summary includes `repeatCount` and `repeatsStarted`. JUnit stays one document, with file testsuite names suffixed by `(repeat N/count)`. Every iteration retains a separate coverage and artifact run; their manifests include `repeatIndex` and `repeatCount` (both `1` for ordinary runs). Profiles stay separate. Failure history is saved once after the batch, combining all observed failed case names without duplicates; suite/file fallbacks take precedence. A later passing iteration cannot clear an earlier failure, and a no-match iteration preserves the existing destination history. Watch runs a complete bounded batch on each change and reloads configured `repeat`; stopping watch waits for the current batch to finish.

`--coverage directory` builds test executables with Go's atomic statement instrumentation and saves results under a new `run-*` directory on every invocation. It requires the Go toolchain used for Kinmokusei. Each selected file has its own `file-0001`, `file-0002`, etc. directory containing raw `covmeta.*`/`covcounters.*` data and a Go-format `coverage.out` profile when counters are available. `manifest.json` records the selected file order and each file's `file`, `data`, `profile`, `executed`, `available`, and `error` fields; paths to artifacts are absolute. Existing runs are retained, including separate watch iterations. An empty assigned shard produces an empty manifest.

Text output prints `COVERAGE` with the manifest path. JSON adds a `coverage` event before the summary, with `directory`, `manifest`, `source: "generated-go"`, and `error`. JUnit remains one XML document; find its coverage artifacts in the requested directory. Coverage supports filters, sharding, shuffling, parallel execution, bail, timeouts, and watch. Files not launched because of bail have `executed: false`; compile errors, panics, and killed processes can leave `available: false` with a reason. Normal exit, including a failed assertion followed by `runAndExit`, retains counters. Missing counters from an otherwise successful process, conversion errors, and output write errors return status `2`; ordinary test failures retain status `1`.

Coverage measures generated Go statements, including test bodies, compiler support code, and any Kinmokusei library code emitted into the same package. Go's default build coverage selects packages in the generated main module; separately imported Go dependencies are excluded by default. Existing `GOFLAGS` are preserved, with `-cover -covermode=atomic` added, so an explicit `-coverpkg` can change the measured packages. The runner overrides `GOCOVERDIR` for each test process and suppresses coverage output from the compiler process.

Use the manifest's `data` directory with Go's metadata-based tools, for example:

```sh
go tool covdata percent -i=.coverage/<run-directory>/file-0001
go tool covdata func -i=.coverage/<run-directory>/file-0001
```

These percentages describe one test file's executable, including its imported application code. They are not `.km` line coverage or a combined application coverage percentage. Separate test files can produce different generated code under the same Go package/file names, so their profiles are kept separate and must not be blindly merged. Profiles refer to generated Go paths, and the compiler can overwrite those sources on a later build. Combine `--coverage` with `--artifacts` to retain matching generated sources and obtain HTML reports. Accurate `.km` mapping and project-wide aggregation remain future work.

`--artifacts directory` saves each invocation in a new `run-*` directory, with one `file-0001`, `file-0002`, etc. directory per selected test file. `manifest.json` records the file order, execution state, source availability, and artifact paths.

Each file also records `status` (`pass`, `fail`, or `not-run`), `failure` (the same process/report diagnostic as the JSON file event), and `durationNs` (compilation plus execution time). These fields preserve abnormal exits, timeouts, and malformed or unreadable reports even when the saved suite report is empty or appears successful. A failed execution can have an empty diagnostic, so use `status` to determine its outcome. Unexecuted files have `status: "not-run"`, an empty failure, and zero duration. The existing `error` field describes artifact collection failures separately from the test result; a collection failure retains the execution outcome when the manifest can still be written. These fields are additions to manifest version `1`; older manifests omit them.

Each executed file retains merged build/test output in `output.txt` and the original suite-report bytes in `suites.jsonl`, including malformed reports. Successful builds also retain `.source/generated.go` and `source.json`, which records the original path, archived path, executable package import path, and source SHA-256. The source is copied before the compilation lock is released, so parallel files retain their own generated code even when later builds overwrite the compiler's shared file.

Artifacts support filters, sharding, shuffling, bail, timeouts, all reporters, and watch. Compile failures retain their output and have no source archive; panics and timed-out executions retain the source captured before execution. Files not launched because of bail remain marked unexecuted. Archive, output, or manifest write failures return status `2`; ordinary failed tests still return `1`. Artifact mode builds executables and captures output even with one worker, so text output is displayed after each file finishes. Text adds `ARTIFACTS` with the manifest path; JSON adds an `artifacts` event before the summary; JUnit remains one XML document.

When combined with `--coverage`, the artifact manifest links to the coverage run. Each file with available counters also receives an additional `coverage.out` whose generated-package paths refer to its archived source, and a standalone `coverage.html`. The original raw coverage data and profile remain unchanged. The retained profile works with `go tool cover -func` and `-html` after later compiler writes. Additional Go packages selected through `-coverpkg` retain their original source references; their sources must remain resolvable for HTML generation. These reports still measure generated Go, and separate executable profiles remain separate.

Every watch iteration retains a separate artifact run. Generated sources are stored under the hidden `.source` directory, and the other artifact file types are excluded from watch scans, so an artifact directory inside the project does not trigger extra runs. Old runs are retained until the user removes them. Source capture currently follows Kinmokusei 0.4.3's `.kinmokusei/gen/generated.go` layout and protects builds within one runner process; independent compiler/runner processes sharing that directory still need separate project copies.

`--watch` runs the selected tests immediately, then repeats them when source, package configuration, or snapshots change. It scans the current project directory recursively for `.km`, `.go`, and `.snap` files, plus `go.mod`, `go.sum`, `kinmokusei.toml`, and `kinmokusei.lock`. A test input outside the current directory also adds its directory to the scan. Hidden directories, `node_modules`, `vendor`, `build`, and `dist` are excluded. Regular file links are watched by their target contents, including target deletion and restoration; directory links and special files are skipped. Other test data and dependencies outside these roots are not watched.

Watch compares file contents, so same-size edits with an unchanged modification timestamp still trigger a run. Additions, deletions, and renames also trigger a run. `--watch-interval duration` sets the positive polling interval, defaulting to `250ms`, and requires `--watch`. Two consecutive scans must see the same contents before another run starts. Each change reruns the full selected test set with the original filter, shard, shuffle, jobs, timeout, and bail options; runs never overlap. Changes made during a run are checked afterward. Failing tests, compile errors, and missing test inputs keep the watcher running so a later edit can repair them. A scan error stops watch with exit status `2`.

Text reporting adds `WATCH run N` before each iteration. JSON reporting emits a `watch` event with an `iteration` number before that iteration's ordinary suite, file, and summary events. `--watch` cannot be combined with `--list` or JUnit reporting. With `--update-snapshots`, snapshots are excluded from the watch scan so Vasest's own writes do not trigger extra runs. Source and configuration changes still rerun tests and update selected snapshots. Stop watch with Ctrl+C or SIGTERM; it schedules no further iteration and waits for the current runner call to finish. Its exit status is the last run's status. Use `--timeout` to bound individual test execution.

`--reporter json` writes one JSON object per line to standard output. A `suite` event contains the suite name, counts, and individual test results under `report.summary`; its `report.success` flag records the suite outcome. A `file` event contains the file path, pass/fail status, captured test output, and error text. The final `summary` event contains file counts, the no-match flag, the shuffle seed, the shard selection when used, and the requested `jobs` count. Test output is captured inside the file event so every standard-output line remains valid JSON. Compile errors and timeouts appear as failed file events even when no suite could report. If a process failure and a malformed suite report occur together, the file error retains both diagnostics separated by a newline; text and JUnit reporting preserve the same causes. The default reporter is `text`.

JUnit XML is available with `--reporter junit`. It writes one document to standard output with a testsuite for each executed file and a testcase for each reported case. Failed assertions become failure elements; skipped and todo cases become skipped elements. With `--fail-on-todo`, todo cases become failures. Compile errors, timeouts, no matching files, and a filter with no matches also produce failure cases. Test output is included as system-out. The runner still exits nonzero when tests fail. JUnit reporting cannot be combined with `--list`.

An abnormal process exit, timeout or invalid suite report adds a `<file>` failure even when an earlier reported case already failed. If the process exits abnormally and its suite report is invalid, that file failure retains both diagnostics. A report that cannot be read returns status `2` and stops further scheduling and repeats; any accompanying process failure remains in the file diagnostic in every reporter. Its failure history selects the whole file. Captured output remains available, but case results cannot be recovered from an unreadable report. The ordinary framework-marked `runAndExit()` failure exit remains represented by its reported cases, without an extra file failure. Completed cases keep their original failure details and locations; native panic output remains in `system-out`.

`TestResult.durationNs` measures a runnable case from the start of `beforeEach` through `afterEach` and registered cleanup; cases skipped before execution and todo cases have zero duration; a runtime-skipped case includes its executed setup and cleanup time. `Summary.durationNs` measures the whole suite, including suite hooks. JSON file events also include `durationNs`, covering compilation and execution, including any wait for the compilation slot in parallel mode. These durations use nanoseconds.

`TestResult.failures` contains individual `TestFailure { phase, message, location }` values in execution order, preserving original error messages, including empty strings. Phases are `beforeAll`, `beforeEach`, `body`, `afterEach`, `cleanup`, `fixture` (typed fixture setup or the per-case snapshot environment scope), and `afterAll`. A shared setup or hook failure has its own `<beforeAll>` or `<afterAll>` result; shared cleanup failures join the `<afterAll>` result. Passing, skipped, and todo results have no failure entries. `TestResult.message` remains the combined summary for existing consumers.

JSON suite events include these failure entries. Text output adds phase labels for hook failures, multiple failures, and empty errors; a single body error keeps its concise output. JUnit emits one failure element per failed case, retaining the combined message attribute and placing each phase and original message in its content. Empty errors display as `(empty error message)` in text and JUnit, while their JSON messages remain empty. Reports from earlier versions that omit failure entries are still accepted; unknown phases and failure entries on non-failed cases are rejected.

Assertion value diagnostics retain native `%#v` formatting for ordinary values. Cyclic graphs are summarized with their native type and `<cyclic value>`; deeply nested or large graphs use `<diagnostic traversal limit>`. This applies to general assertions, slice/map values and mock arguments/returns, so formatting a failure can complete and normal cleanup, reporting and later tests can run. Shared references alone are not cycles. Explicit native `GoString` and `Format` methods retain their formatting behavior. These summaries affect diagnostics only; comparison rules, native types and the original values remain unchanged.

`TestLocation { file, line, source }` records an assertion's actual call site. `source: "go"` identifies a Go runtime position; with Kinmokusei 0.4.3 this normally points into `generated.go`. A compiler-provided line directive referring to `.km` produces `source: "kinmokusei"`. Positions are retained through standard wrapped/joined errors and in setup, fixture, hook, and cleanup failures. `waitFor` keeps its last failed assertion's location; if the last error has no location, it reports the `waitFor` call site. Ordinary thrown errors have an empty location unless annotated. An unavailable position is `{ file: "", line: 0, source: "" }`, and earlier reports that omit `location` remain accepted.

To attach a position supplied by the test author, import `withTestLocation` and wrap a callback:

```ts
withTestLocation("tests/arithmetic.test.km", 42, (): void => {
  expectEqual(1 + 1, 3);
});
```

The callback's thrown error receives that file and positive line number with `source: "explicit"`. The author maintains this position; it is not inferred or verified against the source. An outer annotation takes precedence over an inner location. The error message and underlying error chain remain intact, and native Go panics still propagate. File names must be non-empty and contain no line breaks or NUL; the callback must not be nil. Text and JUnit add `at file:line (source)` to each located failure, while JSON exposes the location object. Invalid or incomplete location objects fail report validation.

Vasest reports the test file, suite, test name, assertion message, and available assertion location. Even an exception with an empty message fails its test. Thrown Kinmokusei errors fail the current test while later tests in the same suite continue. A compile error or an uncaught Go panic fails its file; the runner continues with other files. Malformed suite reports and reports whose success flag, case states, or counts disagree also fail the file with every reporter. Automatic `.km` source line mapping still requires compiler support. Generated Go files can be overwritten by later builds. With `--artifacts`, the manifest maps the original generated path to a retained source file with the same line numbers.

## Development

Run the generated-Go unit tests and Kinmokusei external-project test matrix with:

```sh
sh scripts/test.sh
```

The design outline is in [docs/overview.md](docs/overview.md). Generated Go is a build artifact and is not maintained as source.

## License

Apache-2.0
