"use client"

import React from "react"

type ErrorBoundaryProps = {
  children: React.ReactNode
}

type ErrorBoundaryState = {
  hasError: boolean
  error: Error | null
}

export class ErrorBoundary extends React.Component<ErrorBoundaryProps, ErrorBoundaryState> {
  constructor(props: ErrorBoundaryProps) {
    super(props)
    this.state = { hasError: false, error: null }
  }

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { hasError: true, error }
  }

  componentDidCatch(error: Error, errorInfo: React.ErrorInfo) {
    console.error("ErrorBoundary caught:", error, errorInfo)
  }

  handleReset = () => {
    this.setState({ hasError: false, error: null })
  }

  render() {
    if (this.state.hasError) {
      return (
        <div style={{
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          justifyContent: "center",
          minHeight: "100vh",
          padding: "2rem",
          fontFamily: "system-ui, sans-serif",
        }}>
          <h1 style={{ fontSize: "1.5rem", marginBottom: "0.5rem" }}>Something went wrong</h1>
          <p style={{ color: "#666", marginBottom: "1rem", maxWidth: "400px" }}>
            An unexpected error occurred. Please try again or refresh the page.
          </p>
          <details style={{ marginBottom: "1rem", maxWidth: "600px", width: "100%" }}>
            <summary style={{ cursor: "pointer", color: "#888" }}>Error details</summary>
            <pre style={{
              marginTop: "0.5rem",
              padding: "1rem",
              background: "#f5f5f5",
              borderRadius: "4px",
              overflow: "auto",
              fontSize: "0.875rem",
            }}>
              {this.state.error?.message}
              {this.state.error?.stack}
            </pre>
          </details>
          <button
            onClick={this.handleReset}
            style={{
              padding: "0.5rem 1rem",
              background: "#0070f3",
              color: "white",
              border: "none",
              borderRadius: "4px",
              cursor: "pointer",
              fontSize: "0.875rem",
            }}
          >
            Try again
          </button>
        </div>
      )
    }
    return this.props.children
  }
}