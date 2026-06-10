package com.groundwork.bankdemo;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;

/**
 * Bank-demo Spring Boot application. Exposes a small subset of the Temenos Transact REST
 * surface (Customer, Loan, Policy, Audit) plus a generic /demo/query endpoint. Every
 * request goes through Groundwork via the SDK, with a per-persona JWT minted on the fly
 * by {@link com.groundwork.sdk.PersonaTokenMinter}.
 *
 * <p>This application is the visible "bank application" in the demo. It is what an
 * investor or bank CISO sees on the laptop screen; the magic is that every endpoint is
 * a thin pass-through to the Groundwork runtime, which actually enforces who sees what.
 */
@SpringBootApplication
public class BankDemoApplication {

  public static void main(String[] args) {
    SpringApplication.run(BankDemoApplication.class, args);
  }
}
